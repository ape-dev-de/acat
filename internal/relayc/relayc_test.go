package relayc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/ape-dev-de/acat/pkg/protocol"
	"github.com/ape-dev-de/acat/relay"
)

func startRelay(t *testing.T) (*relay.Server, *httptest.Server) {
	t.Helper()
	cfg := relay.DefaultConfig()
	rs := relay.NewServer(cfg, nil)
	hs := httptest.NewServer(rs.Handler())
	t.Cleanup(func() { rs.Shutdown(); hs.Close() })
	return rs, hs
}

func next(t *testing.T, ch <-chan Event, kind EventKind) Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.Kind == kind {
				return e
			}
		case <-timeout:
			t.Fatalf("timeout waiting for event %d", kind)
		}
	}
}

func TestURLs(t *testing.T) {
	for in, want := range map[string]string{
		"wss://relay.example.org":      "wss://relay.example.org/ws",
		"https://relay.example.org/":   "wss://relay.example.org/ws",
		"wss://relay.example.org/ws?x": "wss://relay.example.org/ws",
	} {
		if got, err := WSURL(in); err != nil || got != want {
			t.Errorf("WSURL(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := WSURL("ftp://x"); err == nil {
		t.Error("want error")
	}
	n, l, err := HookURLs("wss://relay.example.org/ws", "a+b/c")
	if err != nil || n != "https://relay.example.org/hook?r=a%2Bb%2Fc" || l != "https://relay.example.org/lifecycle?r=a%2Bb%2Fc" {
		t.Fatalf("%s %s %v", n, l, err)
	}
}

// End to end against the real relay: connect, receive a webhook push,
// reconnect after a relay restart with a new token.
func TestAgainstRealRelay(t *testing.T) {
	rs, hs := startRelay(t)
	c := New(hs.URL, "test")
	c.MinBackoff, c.MaxBackoff = 10*time.Millisecond, 50*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan Event, 16)
	go func() { _ = c.Run(ctx, events) }()

	conn := next(t, events, Connected)
	hook, _, _ := HookURLs(hs.URL, conn.RoutingToken)
	resp, err := http.Post(hook, "application/json", strings.NewReader(`{"value":[{"subscriptionId":"s"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	n := next(t, events, Notification)
	if !strings.Contains(string(n.Payload), `"subscriptionId":"s"`) {
		t.Fatalf("payload %s", n.Payload)
	}

	// Relay restart: all clients get "going away" → reconnect, new token.
	rs.Shutdown()
	next(t, events, Disconnected)
	again := next(t, events, Connected)
	if again.RoutingToken == conn.RoutingToken || again.RoutingToken == "" {
		t.Fatal("expected a new routing token after reconnect")
	}
}

func TestUnsupportedVersionIsFatal(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		var h protocol.Hello
		_ = wsjson.Read(r.Context(), conn, &h)
		_ = wsjson.Write(r.Context(), conn, protocol.Error{Type: protocol.TypeError, Code: protocol.ErrCodeUnsupportedVersion, Message: "need >= 9"})
		_ = conn.Close(websocket.StatusPolicyViolation, "")
	}))
	defer hs.Close()
	c := New(hs.URL, "test")
	events := make(chan Event, 4)
	err := c.Run(context.Background(), events)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("got %v", err)
	}
	if e := next(t, events, Fatal); !errors.Is(e.Err, ErrUnsupportedVersion) {
		t.Fatalf("event %+v", e)
	}
}

func TestBackoffWhileRelayDown(t *testing.T) {
	hs := httptest.NewServer(http.NotFoundHandler())
	url := hs.URL
	hs.Close() // nothing listens
	c := New(url, "test")
	c.MinBackoff, c.MaxBackoff = 5*time.Millisecond, 20*time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	events := make(chan Event, 100)
	err := c.Run(ctx, events)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if len(events) < 3 {
		t.Fatalf("expected several reconnect attempts, got %d", len(events))
	}
}

// deafRelay completes the handshake and then never reads, so pings are
// never answered (like a half-open TCP connection after a network change).
func deafRelay(t *testing.T) *httptest.Server {
	t.Helper()
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		var h protocol.Hello
		if wsjson.Read(r.Context(), conn, &h) != nil {
			return
		}
		_ = wsjson.Write(r.Context(), conn, protocol.Welcome{Type: protocol.TypeWelcome, Proto: protocol.RelayVersion, RoutingToken: "tok"})
		<-r.Context().Done()
	}))
	t.Cleanup(hs.Close)
	return hs
}

func TestDeadConnectionIsDetectedByPing(t *testing.T) {
	hs := deafRelay(t)
	c := New(hs.URL, "test")
	c.MinBackoff, c.MaxBackoff = 10*time.Millisecond, 50*time.Millisecond
	c.PingInterval, c.PingTimeout = 50*time.Millisecond, 100*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan Event, 16)
	go func() { _ = c.Run(ctx, events) }()
	next(t, events, Connected)
	next(t, events, Disconnected)
	next(t, events, Connected)
}

func TestSleepTriggersReconnect(t *testing.T) {
	_, hs := startRelay(t)
	c := New(hs.URL, "test")
	c.MinBackoff, c.MaxBackoff = 10*time.Millisecond, 50*time.Millisecond
	c.PingInterval, c.PingTimeout = 20*time.Millisecond, time.Second
	var calls atomic.Int64
	c.clock = func() (time.Duration, time.Duration) {
		n := time.Duration(calls.Add(1))
		mono := n * 20 * time.Millisecond
		if n >= 3 { // between tick 2 and 3 the "system slept" for an hour
			return mono + time.Hour, mono
		}
		return mono, mono
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan Event, 16)
	go func() { _ = c.Run(ctx, events) }()
	first := next(t, events, Connected)
	next(t, events, Disconnected)
	if again := next(t, events, Connected); again.RoutingToken == first.RoutingToken {
		t.Fatal("expected a fresh session after sleep")
	}
}
