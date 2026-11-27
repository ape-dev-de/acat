package relay

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/ape-dev-de/acat/pkg/protocol"
)

const (
	kindNotification = "notification"
	kindLifecycle    = "lifecycle"

	maxValidationTokenLen = 4096
	hookReadTimeout       = 10 * time.Second
)

// hookHandler serves /hook (change notifications) and /lifecycle
// (lifecycle events). Contract:
//   - validationToken in the query → echo it as text/plain, 200 (Graph
//     subscription handshake; must happen within 10 s).
//   - otherwise read the body, forward it to the WebSocket owning ?r=,
//     and answer 202 immediately. Unknown tokens also get 202, so the
//     endpoint cannot be used to probe for valid tokens.
//
// Nothing about the request is logged.
func (s *Server) hookHandler(kind string) http.HandlerFunc {
	frameType := protocol.TypeNotification
	if kind == kindLifecycle {
		frameType = protocol.TypeLifecycle
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")

		q := r.URL.Query()
		if vt := q.Get("validationToken"); vt != "" {
			if len(vt) > maxValidationTokenLen {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			s.m.validations.inc(kind)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, vt) //nolint:gosec // Graph contract requires echoing the token; served as text/plain + nosniff
			return
		}

		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(hookReadTimeout))
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				s.m.dropped.inc(reasonTooLarge)
				http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
				return
			}
			s.m.dropped.inc(reasonInvalid)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		s.deliver(kind, frameType, q.Get("r"), body)
		w.WriteHeader(http.StatusAccepted)
	}
}

// deliver forwards a Graph payload to the client that owns token. It never
// blocks: a client whose queue is full is disconnected (it will reconnect
// and resync via Graph).
func (s *Server) deliver(kind, frameType, token string, body []byte) {
	if !json.Valid(body) {
		s.m.dropped.inc(reasonInvalid)
		return
	}
	c := s.hub.get(token)
	if c == nil {
		s.m.dropped.inc(reasonUnknownToken)
		return
	}
	if !c.hookLim.Allow() {
		s.m.dropped.inc(reasonRateLimit)
		return
	}
	frame, err := json.Marshal(protocol.Push{Type: frameType, Payload: body})
	if err != nil {
		s.m.dropped.inc(reasonInvalid)
		return
	}
	select {
	case c.send <- frame:
		s.m.forwarded.inc(kind)
	default:
		s.m.dropped.inc(reasonSlowConsumer)
		c.kick(protocol.CloseSlowConsumer)
	}
}
