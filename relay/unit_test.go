package relay

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestClientIP(t *testing.T) {
	trusted, _ := parsePrefixes("10.0.0.0/8, 192.168.1.1")
	tests := []struct {
		name, remote, xff, want string
	}{
		{"direct, xff ignored", "203.0.113.7:1234", "1.2.3.4", "203.0.113.7"},
		{"trusted proxy", "10.0.0.5:1234", "198.51.100.9", "198.51.100.9"},
		{"chain, rightmost untrusted wins", "10.0.0.5:1", "6.6.6.6, 198.51.100.9, 192.168.1.1", "198.51.100.9"},
		{"trusted, no xff", "10.0.0.5:1", "", "10.0.0.5"},
		{"trusted, garbage xff", "10.0.0.5:1", "nonsense", "10.0.0.5"},
		{"ipv6 mapped", "[::ffff:203.0.113.7]:1", "", "203.0.113.7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remote
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			if got := clientIP(r, trusted); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestParsePrefixes(t *testing.T) {
	p, err := parsePrefixes("10.1.2.3/8,::1, 172.16.0.0/12")
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("::1/128"), netip.MustParsePrefix("172.16.0.0/12")}
	if len(p) != len(want) {
		t.Fatalf("got %v", p)
	}
	for i := range want {
		if p[i] != want[i] {
			t.Fatalf("got %v, want %v", p, want)
		}
	}
	if _, err := parsePrefixes("not-an-ip"); err == nil {
		t.Fatal("want error")
	}
}

func TestKeyedLimiterSweepsIdleEntries(t *testing.T) {
	now := time.Unix(0, 0)
	k := newKeyedLimiter(1, 1)
	k.now = func() time.Time { return now }
	if !k.allow("a") || k.allow("a") {
		t.Fatal("burst of 1 not enforced")
	}
	k.allow("b")
	now = now.Add(10 * time.Minute)
	k.allow("c") // triggers sweep
	if k.size() != 1 {
		t.Fatalf("size %d after sweep, want 1", k.size())
	}
}

func TestHubPerIPAccounting(t *testing.T) {
	h := newHub(2)
	a := newClient("a", "ip", 1, 1, 1)
	b := newClient("b", "ip", 1, 1, 1)
	c := newClient("c", "ip", 1, 1, 1)
	if h.register(a) != nil || h.register(b) != nil {
		t.Fatal("register failed")
	}
	if h.register(c) == nil {
		t.Fatal("cap not enforced")
	}
	h.unregister(a)
	h.unregister(a) // idempotent
	if h.register(c) != nil {
		t.Fatal("slot not freed")
	}
	h.unregister(b)
	h.unregister(c)
	if len(h.perIP) != 0 || h.len() != 0 {
		t.Fatalf("leak: perIP=%v", h.perIP)
	}
}

func TestConfigFromEnvFull(t *testing.T) {
	c, err := ConfigFromEnv(env(map[string]string{
		"ACAT_TRUSTED_PROXIES":  "10.0.0.0/8",
		"ACAT_MAX_BODY_BYTES":   "2048",
		"ACAT_MAX_CONNS_PER_IP": "3",
		"ACAT_METRICS_ADDR":     "127.0.0.1:9090",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxBodyBytes != 2048 || c.MaxConnsPerIP != 3 || len(c.TrustedProxies) != 1 || c.MetricsAddr == "" {
		t.Fatalf("%+v", c)
	}
	for _, bad := range []map[string]string{
		{"ACAT_MAX_BODY_BYTES": "10"},
		{"ACAT_MAX_CONNS_PER_IP": "zero"},
		{"ACAT_TRUSTED_PROXIES": "x"},
	} {
		if _, err := ConfigFromEnv(env(bad)); err == nil {
			t.Fatalf("want error for %v", bad)
		}
	}
}

func FuzzHook(f *testing.F) {
	f.Add("tok", "", sampleNotification)
	f.Add("", "abc", "")
	f.Add("x", "", "{")
	f.Add("x", "", strings.Repeat("[", 5000))
	s := NewServer(DefaultConfig(), nil)
	h := s.Handler()
	f.Fuzz(func(t *testing.T, token, validation, body string) {
		target := "/hook?r=" + url.QueryEscape(token)
		if validation != "" {
			target += "&validationToken=" + url.QueryEscape(validation)
		}
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		switch rec.Code {
		case http.StatusOK, http.StatusAccepted, http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		default:
			t.Fatalf("unexpected status %d", rec.Code)
		}
	})
}
