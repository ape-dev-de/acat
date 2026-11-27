package relay

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Vector computed independently with openssl:
//
//	body='{"token":"t","appId":"a","userId":"u"}'
//	hash=$(printf '%s' "$body" | openssl dgst -sha256 -binary | base64)
//	printf 'POST\n/teamsUser/:exchangeAccessToken?api-version=2023-10-01\n<date>;contoso.communication.azure.com;'"$hash" |
//	  openssl dgst -sha256 -mac HMAC -macopt key:acat-test-access-key-0123456789abcdef -binary | base64
func TestSignACSKnownVector(t *testing.T) {
	key, _ := base64.StdEncoding.DecodeString("YWNhdC10ZXN0LWFjY2Vzcy1rZXktMDEyMzQ1Njc4OWFiY2RlZg==")
	body := []byte(`{"token":"t","appId":"a","userId":"u"}`)
	req, _ := http.NewRequest(http.MethodPost, "https://contoso.communication.azure.com/teamsUser/:exchangeAccessToken?api-version=2023-10-01", bytes.NewReader(body))
	signACS(req, body, key, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))

	if got := req.Header.Get("x-ms-date"); got != "Thu, 01 Jan 2026 12:00:00 GMT" {
		t.Fatalf("date %q", got)
	}
	if got := req.Header.Get("x-ms-content-sha256"); got != "I85g5Ub9a3ndZEoRIwK183mLR3FHZ0pYpPmZMYPB964=" {
		t.Fatalf("hash %q", got)
	}
	want := "HMAC-SHA256 SignedHeaders=x-ms-date;host;x-ms-content-sha256&Signature=2UEuZZVgzdqJ7ehg1FbWMFlgMsWGBB7DVdfjVm4+pcE="
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("authorization\n got %s\nwant %s", got, want)
	}
}

func TestParseACSConnectionString(t *testing.T) {
	c, err := ParseACSConnectionString("endpoint=https://contoso.communication.azure.com/;accesskey=YWJj")
	if err != nil || c.Endpoint.Host != "contoso.communication.azure.com" || string(c.AccessKey) != "abc" {
		t.Fatalf("%+v %v", c, err)
	}
	for _, bad := range []string{"", "endpoint=http://x/;accesskey=YWJj", "endpoint=https://x/", "endpoint=https://x/;accesskey=!!"} {
		if _, err := ParseACSConnectionString(bad); err == nil {
			t.Errorf("want error for %q", bad)
		}
	}
}

func fakeJWT(claims map[string]string) string {
	b, _ := json.Marshal(claims)
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
}

// fakeACS verifies the HMAC independently of signACS's string building
// and returns a token.
func fakeACS(t *testing.T, key []byte, status int) *httptest.Server {
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/teamsUser/:exchangeAccessToken" || r.URL.Query().Get("api-version") != ACSAPIVersion {
			t.Errorf("path %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		sum := sha256.Sum256(body)
		hash := base64.StdEncoding.EncodeToString(sum[:])
		if r.Header.Get("x-ms-content-sha256") != hash {
			t.Error("content hash mismatch")
		}
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte("POST\n" + r.URL.RequestURI() + "\n" + r.Header.Get("x-ms-date") + ";" + r.Host + ";" + hash))
		want := "HMAC-SHA256 SignedHeaders=x-ms-date;host;x-ms-content-sha256&Signature=" + base64.StdEncoding.EncodeToString(mac.Sum(nil))
		if r.Header.Get("Authorization") != want {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req map[string]string
		_ = json.Unmarshal(body, &req)
		if req["appId"] != "app-1" || req["userId"] != "oid-1" || req["token"] == "" {
			t.Errorf("acs body %v", req)
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_, _ = io.WriteString(w, `{"token":"acs-token-xyz","expiresOn":"2026-01-01T13:00:00Z"}`)
	}))
}

func newACSRelay(t *testing.T, acsStatus int, mutate func(*Config)) (*Server, *httptest.Server) {
	t.Helper()
	key := []byte("relay-test-key")
	acs := fakeACS(t, key, acsStatus)
	t.Cleanup(acs.Close)
	cfg := DefaultConfig()
	cfg.ACSConnectionString = "endpoint=" + acs.URL + "/;accesskey=" + base64.StdEncoding.EncodeToString(key)
	if mutate != nil {
		mutate(&cfg)
	}
	s := NewServer(cfg, nil)
	s.acsHTTP = acs.Client()
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(hs.Close)
	return s, hs
}

func postToken(t *testing.T, hs *httptest.Server, token string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"token": token})
	resp, err := http.Post(hs.URL+"/acs/token", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Error("missing no-store")
	}
	return resp.StatusCode, string(b)
}

var goodClaims = map[string]string{"oid": "oid-1", "tid": "tenant-a", "appid": "app-1"}

func TestACSTokenExchange(t *testing.T) {
	s, hs := newACSRelay(t, http.StatusOK, nil)
	code, body := postToken(t, hs, fakeJWT(goodClaims))
	if code != 200 || !strings.Contains(body, `"token":"acs-token-xyz"`) || !strings.Contains(body, "2026-01-01T13:00:00Z") {
		t.Fatalf("%d %s", code, body)
	}
	if s.m.acs.get(acsOK) != 1 {
		t.Fatal("metric")
	}
	// v2 token: azp instead of appid
	code, _ = postToken(t, hs, fakeJWT(map[string]string{"oid": "oid-1", "tid": "tenant-a", "azp": "app-1"}))
	if code != 200 {
		t.Fatalf("v2 token: %d", code)
	}
	if len(s.features) != 1 || s.features[0] != "acs-token" {
		t.Fatalf("features %v", s.features)
	}
}

func TestACSRejections(t *testing.T) {
	_, hs := newACSRelay(t, http.StatusOK, func(c *Config) {
		c.AllowedTenants = []string{"tenant-a"}
		c.AllowedAppIDs = []string{"app-1"}
	})
	if code, _ := postToken(t, hs, "not-a-jwt"); code != 400 {
		t.Fatalf("garbage: %d", code)
	}
	if code, _ := postToken(t, hs, fakeJWT(map[string]string{"oid": "x"})); code != 400 {
		t.Fatalf("missing claims: %d", code)
	}
	other := map[string]string{"oid": "oid-1", "tid": "tenant-b", "appid": "app-1"}
	if code, _ := postToken(t, hs, fakeJWT(other)); code != 403 {
		t.Fatalf("foreign tenant: %d", code)
	}
	other = map[string]string{"oid": "oid-1", "tid": "TENANT-A", "appid": "evil-app"}
	if code, _ := postToken(t, hs, fakeJWT(other)); code != 403 {
		t.Fatalf("foreign app: %d", code)
	}
	if code, _ := postToken(t, hs, fakeJWT(goodClaims)); code != 200 {
		t.Fatalf("allowed: %d", code)
	}
}

func TestACSUpstreamErrors(t *testing.T) {
	_, hs := newACSRelay(t, http.StatusUnauthorized, nil)
	if code, _ := postToken(t, hs, fakeJWT(goodClaims)); code != 403 {
		t.Fatalf("acs 401 → %d, want 403", code)
	}
	_, hs = newACSRelay(t, http.StatusInternalServerError, nil)
	if code, _ := postToken(t, hs, fakeJWT(goodClaims)); code != 502 {
		t.Fatalf("acs 500 → %d, want 502", code)
	}
}

func TestACSNotConfiguredAndRateLimit(t *testing.T) {
	hs := httptest.NewServer(NewServer(DefaultConfig(), nil).Handler())
	defer hs.Close()
	if code, _ := postToken(t, hs, fakeJWT(goodClaims)); code != http.StatusNotImplemented {
		t.Fatalf("unconfigured: %d", code)
	}

	s, hs2 := newACSRelay(t, http.StatusOK, func(c *Config) { c.ACSRatePerMin = 4 }) // burst 1
	_ = s
	if code, _ := postToken(t, hs2, fakeJWT(goodClaims)); code != 200 {
		t.Fatalf("first: %d", code)
	}
	if code, _ := postToken(t, hs2, fakeJWT(goodClaims)); code != 429 {
		t.Fatalf("second: %d", code)
	}
}

func TestACSLeavesNoTraces(t *testing.T) {
	logs := &syncBuffer{}
	key := []byte("k")
	acs := fakeACS(t, key, http.StatusOK)
	defer acs.Close()
	cfg := DefaultConfig()
	cfg.ACSConnectionString = "endpoint=" + acs.URL + "/;accesskey=" + base64.StdEncoding.EncodeToString(key)
	s := NewServer(cfg, newTestLogger(logs))
	s.acsHTTP = acs.Client()
	hs := httptest.NewServer(s.Handler())
	defer hs.Close()
	jwt := fakeJWT(goodClaims)
	postToken(t, hs, jwt)
	var mb bytes.Buffer
	s.m.write(&mb)
	for _, secret := range []string{jwt, "acs-token-xyz", "oid-1", "tenant-a"} {
		if strings.Contains(logs.String()+mb.String(), secret) {
			t.Errorf("leaked %q", secret)
		}
	}
	u, _ := url.Parse(acs.URL)
	if strings.Contains(logs.String(), u.Host) {
		t.Error("upstream host logged")
	}
}

func FuzzACSClaims(f *testing.F) {
	f.Add("a.eyJvaWQiOiJvIiwidGlkIjoidCIsImFwcGlkIjoiYSJ9.c")
	f.Add("..")
	f.Add("x.!!!.y")
	f.Fuzz(func(t *testing.T, jwt string) {
		c, err := parseClaims(jwt)
		if err == nil && (c.OID == "" || c.TID == "" || c.AppID == "") {
			t.Fatalf("accepted claims without oid/tid/appid: %+v", c)
		}
	})
}

func FuzzACSConnectionString(f *testing.F) {
	f.Add("endpoint=https://x.communication.azure.com/;accesskey=YWJj")
	f.Add(";;=;endpoint=")
	f.Fuzz(func(t *testing.T, s string) {
		c, err := ParseACSConnectionString(s)
		if err == nil && (c.Endpoint == nil || len(c.AccessKey) == 0) {
			t.Fatalf("accepted incomplete connection string: %+v", c)
		}
	})
}
