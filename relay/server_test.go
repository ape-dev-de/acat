package relay

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestConfigFromEnv(t *testing.T) {
	c, err := ConfigFromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != ":8080" || c.TLSMode != TLSModeProxy {
		t.Fatalf("unexpected defaults: %+v", c)
	}

	if _, err := ConfigFromEnv(env(map[string]string{"ACAT_TLS_MODE": "autocert"})); err == nil {
		t.Fatal("autocert without domain must fail")
	}
	if _, err := ConfigFromEnv(env(map[string]string{"ACAT_TLS_MODE": "bogus"})); err == nil {
		t.Fatal("unknown TLS mode must fail")
	}
	if _, err := ConfigFromEnv(env(map[string]string{"ACAT_TLS_MODE": "autocert", "ACAT_DOMAIN": "relay.example.org"})); err != nil {
		t.Fatal(err)
	}
}

func TestHealthz(t *testing.T) {
	srv := httptest.NewServer(NewServer(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
