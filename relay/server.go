// Package relay implements acat-relay: a stateless, zero-knowledge bridge
// from Microsoft Graph webhooks to client WebSocket connections.
//
// M0 only provides configuration and health endpoints; hook, lifecycle, ws
// and acs routes follow in M5/M8 (see docs/plan.md).
package relay

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// TLS modes.
const (
	TLSModeProxy    = "proxy"    // plain HTTP behind a reverse proxy
	TLSModeAutocert = "autocert" // terminate TLS via Let's Encrypt
)

// Config is read exclusively from environment variables.
type Config struct {
	ListenAddr string // ACAT_LISTEN_ADDR, default ":8080"
	TLSMode    string // ACAT_TLS_MODE, default "proxy"
	Domain     string // ACAT_DOMAIN, required for autocert
}

// ConfigFromEnv builds a Config from a getenv function (os.Getenv in prod).
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	c := Config{
		ListenAddr: getenv("ACAT_LISTEN_ADDR"),
		TLSMode:    getenv("ACAT_TLS_MODE"),
		Domain:     getenv("ACAT_DOMAIN"),
	}
	if c.ListenAddr == "" {
		c.ListenAddr = ":8080"
	}
	if c.TLSMode == "" {
		c.TLSMode = TLSModeProxy
	}
	switch c.TLSMode {
	case TLSModeProxy:
	case TLSModeAutocert:
		if c.Domain == "" {
			return c, errors.New("ACAT_DOMAIN is required when ACAT_TLS_MODE=autocert")
		}
	default:
		return c, fmt.Errorf("unknown ACAT_TLS_MODE %q (want %q or %q)", c.TLSMode, TLSModeProxy, TLSModeAutocert)
	}
	return c, nil
}

// Server holds relay state. All state lives in memory only.
type Server struct {
	cfg Config
	log *slog.Logger
}

// NewServer creates a relay server.
func NewServer(cfg Config, log *slog.Logger) *Server {
	return &Server{cfg: cfg, log: log}
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}
