// Command acat-relay is the zero-knowledge webhook relay.
// All configuration comes from environment variables; see
// deploy/relay.env.example and docs/self-hosting.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/ape-dev-de/acat/internal/version"
	"github.com/ape-dev-de/acat/relay"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.String("acat-relay"))
		return
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("relay stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := relay.ConfigFromEnv(os.Getenv)
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	rs := relay.NewServer(cfg, log)

	newServer := func(addr string, h http.Handler) *http.Server {
		return &http.Server{
			Addr:              addr,
			Handler:           h,
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    16 << 10,
			// No ErrorLog output: net/http error lines can contain remote
			// addresses; keep the relay quiet by design.
			ErrorLog: slog.NewLogLogger(slog.DiscardHandler, slog.LevelError),
		}
	}

	var servers []*http.Server
	errc := make(chan error, 3)
	serve := func(s *http.Server, tls bool) {
		servers = append(servers, s)
		go func() {
			var err error
			if tls {
				err = s.ListenAndServeTLS("", "")
			} else {
				err = s.ListenAndServe()
			}
			if !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
	}

	pub := newServer(cfg.ListenAddr, rs.Handler())
	pub.RegisterOnShutdown(rs.Shutdown)

	switch cfg.TLSMode {
	case relay.TLSModeAutocert:
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.Domain),
			Cache:      autocert.DirCache(cfg.CertDir),
		}
		pub.Addr = ":443"
		pub.TLSConfig = m.TLSConfig()
		serve(newServer(":80", m.HTTPHandler(nil)), false) // ACME http-01 + redirect
		serve(pub, true)
		log.Info("listening", "mode", cfg.TLSMode, "domain", cfg.Domain, "version", version.Version)
	default:
		serve(pub, false)
		log.Info("listening", "mode", cfg.TLSMode, "addr", cfg.ListenAddr, "version", version.Version)
	}

	if cfg.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", rs.MetricsHandler())
		serve(newServer(cfg.MetricsAddr, mux), false)
		log.Info("metrics listening", "addr", cfg.MetricsAddr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(shutdownCtx)
	}
	return nil
}
