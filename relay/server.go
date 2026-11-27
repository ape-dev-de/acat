// Package relay implements acat-relay: a stateless, zero-knowledge bridge
// from Microsoft Graph webhooks to client WebSocket connections.
//
// Routes:
//
//	/hook       Graph change notifications (?r=<routingToken>)
//	/lifecycle  Graph lifecycle notifications (?r=<routingToken>)
//	/ws         client WebSocket (hello → welcome{routingToken} → push frames)
//	/healthz    liveness
//
// Metrics are served on a separate listener (Config.MetricsAddr).
//
// The relay keeps no state except routingToken → connection, in memory.
// It never logs request bodies, IDs, tokens or query strings.
package relay

import (
	"io"
	"log/slog"
	"net/http"
)

// Server holds relay state. All state lives in memory only.
type Server struct {
	cfg        Config
	log        *slog.Logger
	hub        *hub
	m          *metrics
	connectLim *keyedLimiter
	features   []string
}

// NewServer creates a relay server. log may be nil.
func NewServer(cfg Config, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Server{
		cfg:        cfg,
		log:        log,
		hub:        newHub(cfg.MaxConnsPerIP),
		m:          newMetrics(),
		connectLim: newKeyedLimiter(cfg.WSConnectRate, cfg.WSConnectBurst),
	}
}

// Handler returns the public HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", s.hookHandler(kindNotification))
	mux.HandleFunc("/lifecycle", s.hookHandler(kindLifecycle))
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}

// MetricsHandler returns the Prometheus endpoint (serve it privately).
func (s *Server) MetricsHandler() http.Handler { return s.m.handler() }

// Shutdown closes all client connections so clients reconnect elsewhere or
// after the restart. http.Server.Shutdown does not touch hijacked
// WebSocket connections, so call this from RegisterOnShutdown.
func (s *Server) Shutdown() { s.hub.kickAll("server shutdown") }
