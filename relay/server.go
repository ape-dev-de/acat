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
	"time"

	"github.com/ape-dev-de/acat/pkg/protocol"
)

// Server holds relay state. All state lives in memory only.
type Server struct {
	cfg        Config
	log        *slog.Logger
	hub        *hub
	m          *metrics
	connectLim *keyedLimiter
	features   []string

	acs     *ACSConfig
	acsHTTP *http.Client
	acsLim  *keyedLimiter
	now     func() time.Time
}

// NewServer creates a relay server. log may be nil. An invalid ACS
// connection string disables /acs/token (ConfigFromEnv rejects it earlier).
func NewServer(cfg Config, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s := &Server{
		cfg:        cfg,
		log:        log,
		hub:        newHub(cfg.MaxConnsPerIP),
		m:          newMetrics(),
		connectLim: newKeyedLimiter(cfg.WSConnectRate, cfg.WSConnectBurst),
		acsHTTP:    &http.Client{Timeout: acsUpstreamLimit},
		acsLim:     newKeyedLimiter(cfg.ACSRatePerMin/60, max(int(cfg.ACSRatePerMin/4), 1)),
		now:        time.Now,
	}
	if cfg.ACSConnectionString != "" {
		if acs, err := ParseACSConnectionString(cfg.ACSConnectionString); err == nil {
			s.acs = &acs
			s.features = append(s.features, protocol.FeatureACSToken)
		}
	}
	return s
}

// Handler returns the public HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", s.hookHandler(kindNotification))
	mux.HandleFunc("/lifecycle", s.hookHandler(kindLifecycle))
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.HandleFunc("POST /acs/token", s.handleACSToken)
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
