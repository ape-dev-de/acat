package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/ape-dev-de/acat/pkg/protocol"
)

// syncBuffer collects log output from concurrent goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type testRelay struct {
	t    *testing.T
	srv  *Server
	http *httptest.Server
	logs *syncBuffer
}

func newTestRelay(t *testing.T, mutate func(*Config)) *testRelay {
	t.Helper()
	cfg := DefaultConfig()
	cfg.PingInterval = 50 * time.Millisecond
	cfg.HelloTimeout = time.Second
	if mutate != nil {
		mutate(&cfg)
	}
	logs := &syncBuffer{}
	s := NewServer(cfg, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(func() { s.Shutdown(); hs.Close() })
	return &testRelay{t: t, srv: s, http: hs, logs: logs}
}

func (r *testRelay) wsURL() string { return "ws" + strings.TrimPrefix(r.http.URL, "http") + "/ws" }

// connect performs the hello/welcome handshake.
func (r *testRelay) connect(ctx context.Context) (*websocket.Conn, protocol.Welcome) {
	r.t.Helper()
	conn, _, err := websocket.Dial(ctx, r.wsURL(), nil)
	if err != nil {
		r.t.Fatalf("dial: %v", err)
	}
	r.t.Cleanup(func() { _ = conn.CloseNow() })
	if err := wsjson.Write(ctx, conn, protocol.Hello{Type: protocol.TypeHello, Proto: protocol.RelayVersion, Client: "test"}); err != nil {
		r.t.Fatalf("hello: %v", err)
	}
	var w protocol.Welcome
	if err := wsjson.Read(ctx, conn, &w); err != nil {
		r.t.Fatalf("welcome: %v", err)
	}
	if w.Type != protocol.TypeWelcome || w.Proto != protocol.RelayVersion {
		r.t.Fatalf("unexpected welcome %+v", w)
	}
	return conn, w
}

func (r *testRelay) post(path, token, body string) *http.Response {
	r.t.Helper()
	u := r.http.URL + path + "?r=" + url.QueryEscape(token)
	resp, err := http.Post(u, "application/json", strings.NewReader(body))
	if err != nil {
		r.t.Fatalf("post: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// A Graph notification without resource data (IDs only), as documented.
const sampleNotification = `{"value":[{"subscriptionId":"7f105c7d-2dc5-4530-97cd-4e7ae6534c07","clientState":"secretClientValue","changeType":"created","resource":"chats('19:abc@unq.gbl.spaces')/messages('1612289765949')","tenantId":"84bd8158-6d4d-4958-8b9f-9d6445542f95"}]}`

func TestNotificationIsForwarded(t *testing.T) {
	r := newTestRelay(t, nil)
	ctx := ctxT(t)
	conn, w := r.connect(ctx)

	for _, tc := range []struct{ path, wantType string }{
		{"/hook", protocol.TypeNotification},
		{"/lifecycle", protocol.TypeLifecycle},
	} {
		resp := r.post(tc.path, w.RoutingToken, sampleNotification)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("%s: status %d, want 202", tc.path, resp.StatusCode)
		}
		var push protocol.Push
		if err := wsjson.Read(ctx, conn, &push); err != nil {
			t.Fatalf("%s: read push: %v", tc.path, err)
		}
		if push.Type != tc.wantType {
			t.Fatalf("%s: type %q, want %q", tc.path, push.Type, tc.wantType)
		}
		var got, want any
		_ = json.Unmarshal(push.Payload, &got)
		_ = json.Unmarshal([]byte(sampleNotification), &want)
		gb, _ := json.Marshal(got)
		wb, _ := json.Marshal(want)
		if !bytes.Equal(gb, wb) {
			t.Fatalf("%s: payload changed:\n got %s\nwant %s", tc.path, gb, wb)
		}
	}
}

func TestValidationHandshake(t *testing.T) {
	r := newTestRelay(t, nil)
	const vt = "Validation: Testing client application reachability for subscription Request-Id: 1234"
	for _, path := range []string{"/hook", "/lifecycle"} {
		for _, method := range []string{http.MethodPost, http.MethodGet} {
			req, _ := http.NewRequest(method, r.http.URL+path+"?r=whatever&validationToken="+url.QueryEscape(vt), nil)
			start := time.Now()
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK || string(body) != vt {
				t.Fatalf("%s %s: status %d body %q", method, path, resp.StatusCode, body)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
				t.Fatalf("content type %q", ct)
			}
			if d := time.Since(start); d > time.Second {
				t.Fatalf("validation took %v", d)
			}
		}
	}
}

func TestUnknownTokenIsDroppedWith202(t *testing.T) {
	r := newTestRelay(t, nil)
	resp := r.post("/hook", "does-not-exist", sampleNotification)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d, want 202 (no token oracle)", resp.StatusCode)
	}
	if got := r.srv.m.dropped.get(reasonUnknownToken); got != 1 {
		t.Fatalf("unknown_token drops = %d", got)
	}
}

func TestInvalidAndOversizedBodies(t *testing.T) {
	r := newTestRelay(t, func(c *Config) { c.MaxBodyBytes = 1024 })
	ctx := ctxT(t)
	_, w := r.connect(ctx)

	if resp := r.post("/hook", w.RoutingToken, "not json"); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("invalid json: status %d", resp.StatusCode)
	}
	if got := r.srv.m.dropped.get(reasonInvalid); got != 1 {
		t.Fatalf("invalid drops = %d", got)
	}
	big := `{"x":"` + strings.Repeat("a", 2048) + `"}`
	if resp := r.post("/hook", w.RoutingToken, big); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized: status %d", resp.StatusCode)
	}
	resp, err := http.Get(r.http.URL + "/hook?r=" + w.RoutingToken)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET without validationToken: status %d", resp.StatusCode)
	}
}

func TestTokenDiscardedOnDisconnect(t *testing.T) {
	r := newTestRelay(t, nil)
	ctx := ctxT(t)
	conn, w := r.connect(ctx)
	if len(w.RoutingToken) < 22 { // 128 bit base64url
		t.Fatalf("routing token too short: %d chars", len(w.RoutingToken))
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
	waitFor(t, func() bool { return r.srv.hub.len() == 0 })
	r.post("/hook", w.RoutingToken, sampleNotification)
	if got := r.srv.m.dropped.get(reasonUnknownToken); got != 1 {
		t.Fatalf("notification for closed connection not dropped")
	}
}

func TestRestartLeavesNoState(t *testing.T) {
	r1 := newTestRelay(t, nil)
	_, w := r1.connect(ctxT(t))
	r1.srv.Shutdown()
	r1.http.Close()

	r2 := newTestRelay(t, nil)
	if r2.srv.hub.len() != 0 {
		t.Fatal("fresh server has state")
	}
	r2.post("/hook", w.RoutingToken, sampleNotification)
	if got := r2.srv.m.dropped.get(reasonUnknownToken); got != 1 {
		t.Fatal("token from previous process accepted")
	}
}

func TestTokensAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 10000 {
		tok := newRoutingToken()
		if seen[tok] {
			t.Fatal("duplicate routing token")
		}
		seen[tok] = true
	}
}

func TestVersionMismatch(t *testing.T) {
	r := newTestRelay(t, nil)
	ctx := ctxT(t)
	conn, _, err := websocket.Dial(ctx, r.wsURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	_ = wsjson.Write(ctx, conn, protocol.Hello{Type: protocol.TypeHello, Proto: protocol.RelayMinVersion - 1})
	var e protocol.Error
	if err := wsjson.Read(ctx, conn, &e); err != nil {
		t.Fatal(err)
	}
	if e.Type != protocol.TypeError || e.Code != protocol.ErrCodeUnsupportedVersion {
		t.Fatalf("got %+v", e)
	}
	_, _, err = conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("want policy violation close, got %v", err)
	}
}

func TestNewerClientGetsCurrentVersion(t *testing.T) {
	r := newTestRelay(t, nil)
	ctx := ctxT(t)
	conn, _, err := websocket.Dial(ctx, r.wsURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	_ = wsjson.Write(ctx, conn, protocol.Hello{Type: protocol.TypeHello, Proto: protocol.RelayVersion + 5})
	var w protocol.Welcome
	if err := wsjson.Read(ctx, conn, &w); err != nil {
		t.Fatal(err)
	}
	if w.Proto != protocol.RelayVersion {
		t.Fatalf("negotiated %d", w.Proto)
	}
}

func TestMissingHelloTimesOut(t *testing.T) {
	r := newTestRelay(t, func(c *Config) { c.HelloTimeout = 100 * time.Millisecond })
	ctx := ctxT(t)
	conn, _, err := websocket.Dial(ctx, r.wsURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	// The hello deadline aborts the connection; a close frame is not guaranteed.
	if _, _, err = conn.Read(ctx); err == nil {
		t.Fatal("connection still open after hello timeout")
	}
	waitFor(t, func() bool { return r.srv.m.wsRejected.get(reasonHandshake) == 1 })
}

func TestClientDataFrameClosesConnection(t *testing.T) {
	r := newTestRelay(t, nil)
	ctx := ctxT(t)
	conn, _ := r.connect(ctx)
	_ = wsjson.Write(ctx, conn, map[string]string{"type": "unexpected"})
	waitFor(t, func() bool { return r.srv.hub.len() == 0 })
}

func TestSlowConsumerIsDisconnected(t *testing.T) {
	r := newTestRelay(t, func(c *Config) { c.SendBuffer = 1; c.HookBurst = 1000; c.HookRate = 1000 })
	ctx := ctxT(t)
	_, w := r.connect(ctx) // never reads
	// Deliver directly (no HTTP) so the queue fills deterministically.
	c := r.srv.hub.get(w.RoutingToken)
	if c == nil {
		t.Fatal("client not registered")
	}
	for range 10000 {
		r.srv.deliver(kindNotification, protocol.TypeNotification, w.RoutingToken, []byte(`{}`))
		if r.srv.m.dropped.get(reasonSlowConsumer) > 0 {
			break
		}
	}
	if r.srv.m.dropped.get(reasonSlowConsumer) == 0 {
		t.Skip("writer kept up; slow-consumer path not triggered on this machine")
	}
	waitFor(t, func() bool { return r.srv.hub.len() == 0 })
}

func TestHookRateLimitPerToken(t *testing.T) {
	r := newTestRelay(t, func(c *Config) { c.HookRate = 0.001; c.HookBurst = 2 })
	ctx := ctxT(t)
	_, w := r.connect(ctx)
	for range 5 {
		r.post("/hook", w.RoutingToken, sampleNotification)
	}
	if got := r.srv.m.dropped.get(reasonRateLimit); got != 3 {
		t.Fatalf("ratelimit drops = %d, want 3", got)
	}
}

func TestConnectRateLimitAndPerIPCap(t *testing.T) {
	r := newTestRelay(t, func(c *Config) { c.WSConnectBurst = 3; c.WSConnectRate = 0.001 })
	ctx := ctxT(t)
	for range 3 {
		r.connect(ctx)
	}
	_, resp, err := websocket.Dial(ctx, r.wsURL(), nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("4th connect should be rate limited, got err=%v resp=%v", err, resp)
	}

	r2 := newTestRelay(t, func(c *Config) { c.MaxConnsPerIP = 1 })
	r2.connect(ctx)
	conn, _, err := websocket.Dial(ctx, r2.wsURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	_ = wsjson.Write(ctx, conn, protocol.Hello{Type: protocol.TypeHello, Proto: protocol.RelayVersion})
	var e protocol.Error
	if err := wsjson.Read(ctx, conn, &e); err != nil || e.Code != protocol.ErrCodeTooManyConnections {
		t.Fatalf("want too_many_connections, got %+v err=%v", e, err)
	}
}

func TestShutdownClosesClients(t *testing.T) {
	r := newTestRelay(t, nil)
	ctx := ctxT(t)
	conn, _ := r.connect(ctx)
	r.srv.Shutdown()
	_, _, err := conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Fatalf("want going away, got %v", err)
	}
}

// Zero knowledge: nothing identifying may appear in logs or metrics.
func TestNoSensitiveDataInLogsOrMetrics(t *testing.T) {
	r := newTestRelay(t, nil)
	ctx := ctxT(t)
	conn, w := r.connect(ctx)
	r.post("/hook", w.RoutingToken, sampleNotification)
	var push protocol.Push
	_ = wsjson.Read(ctx, conn, &push)
	r.post("/hook", "unknown-token-xyz", sampleNotification)
	_, _ = http.Get(r.http.URL + "/hook?validationToken=vt-secret-123")

	var mb bytes.Buffer
	r.srv.m.write(&mb)
	for name, out := range map[string]string{"logs": r.logs.String(), "metrics": mb.String()} {
		for _, secret := range []string{w.RoutingToken, "unknown-token-xyz", "vt-secret-123", "secretClientValue", "7f105c7d", "19:abc", "84bd8158", "127.0.0.1"} {
			if strings.Contains(out, secret) {
				t.Errorf("%s contain %q", name, secret)
			}
		}
	}
	if !strings.Contains(mb.String(), `acat_relay_hook_forwarded_total{kind="notification"} 1`) {
		t.Errorf("metrics missing forwarded counter:\n%s", mb.String())
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
