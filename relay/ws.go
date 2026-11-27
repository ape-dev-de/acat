package relay

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/ape-dev-de/acat/pkg/protocol"
)

const maxClientFrame = 4096

// handleWS runs one client connection:
// hello → welcome{routingToken} → stream of push frames, with pings.
// Clients send nothing after hello in protocol v1; any data frame closes
// the connection.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, s.cfg.TrustedProxies)
	if !s.connectLim.allow(ip) {
		s.m.wsRejected.inc(reasonRateLimit)
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.m.wsRejected.inc(reasonHandshake)
		return
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(maxClientFrame)
	ctx := r.Context()

	var hello protocol.Hello
	hctx, cancel := context.WithTimeout(ctx, s.cfg.HelloTimeout)
	err = wsjson.Read(hctx, conn, &hello)
	cancel()
	if err != nil || hello.Type != protocol.TypeHello {
		s.m.wsRejected.inc(reasonHandshake)
		_ = conn.Close(websocket.StatusPolicyViolation, "expected hello")
		return
	}

	proto, err := protocol.Negotiate(hello.Proto, protocol.RelayMinVersion, protocol.RelayVersion)
	if err != nil {
		s.m.wsRejected.inc(reasonVersion)
		s.writeError(ctx, conn, protocol.ErrCodeUnsupportedVersion, err.Error())
		_ = conn.Close(websocket.StatusPolicyViolation, protocol.ErrCodeUnsupportedVersion)
		return
	}

	c := newClient(newRoutingToken(), ip, s.cfg.SendBuffer, s.cfg.HookRate, s.cfg.HookBurst)
	if err := s.hub.register(c); err != nil {
		s.m.wsRejected.inc(reasonTooMany)
		s.writeError(ctx, conn, protocol.ErrCodeTooManyConnections, err.Error())
		_ = conn.Close(websocket.StatusTryAgainLater, protocol.ErrCodeTooManyConnections)
		return
	}
	defer s.hub.unregister(c)
	s.m.wsAccepted.Add(1)
	s.m.wsActive.Add(1)
	defer s.m.wsActive.Add(-1)

	welcome := protocol.Welcome{
		Type:         protocol.TypeWelcome,
		Proto:        proto,
		RoutingToken: c.token,
		Features:     s.features,
	}
	if err := s.writeJSON(ctx, conn, welcome); err != nil {
		return
	}

	// CloseRead handles control frames (pong, close) and closes the
	// connection if the client sends a data frame.
	readCtx := conn.CloseRead(ctx)
	ping := time.NewTicker(s.cfg.PingInterval)
	defer ping.Stop()

	// A kick aborts an in-flight write (e.g. blocked on a full TCP window).
	writeCtx, abortWrites := context.WithCancel(ctx)
	defer abortWrites()
	go func() {
		select {
		case <-c.done:
			abortWrites()
		case <-writeCtx.Done():
		}
	}()

	for {
		select {
		case <-readCtx.Done():
			return
		case <-c.done:
			// Drop the routing token first: the close handshake can take
			// seconds if the peer does not answer.
			s.hub.unregister(c)
			code := websocket.StatusGoingAway
			if c.closeReason == protocol.CloseSlowConsumer {
				code = websocket.StatusPolicyViolation
			}
			_ = conn.Close(code, c.closeReason)
			return
		case frame := <-c.send:
			wctx, cancel := context.WithTimeout(writeCtx, s.cfg.WriteTimeout)
			err := conn.Write(wctx, websocket.MessageText, frame)
			cancel()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, s.cfg.WriteTimeout)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (s *Server) writeJSON(ctx context.Context, conn *websocket.Conn, v any) error {
	wctx, cancel := context.WithTimeout(ctx, s.cfg.WriteTimeout)
	defer cancel()
	return wsjson.Write(wctx, conn, v)
}

// writeError sends a best-effort error frame before an intentional close.
func (s *Server) writeError(ctx context.Context, conn *websocket.Conn, code, msg string) {
	_ = s.writeJSON(ctx, conn, protocol.Error{Type: protocol.TypeError, Code: code, Message: msg})
}
