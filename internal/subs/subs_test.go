package subs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ape-dev-de/acat/internal/graph"
	"github.com/ape-dev-de/acat/internal/relayc"
	"github.com/ape-dev-de/acat/relay"
)

// fakeGraph stores subscriptions and can deliver notifications to their
// notificationUrl over real HTTP (through the real relay).
type fakeGraph struct {
	mu       sync.Mutex
	subs     map[string]graph.Subscription
	nextID   int
	calls    []string
	failNext map[string]error
}

func newFakeGraph() *fakeGraph {
	return &fakeGraph{subs: map[string]graph.Subscription{}, failNext: map[string]error{}}
}

func (f *fakeGraph) record(call string) error {
	f.calls = append(f.calls, call)
	if err, ok := f.failNext[call]; ok {
		delete(f.failNext, call)
		return err
	}
	return nil
}

func (f *fakeGraph) CreateSubscription(_ context.Context, s graph.Subscription) (graph.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("create"); err != nil {
		return graph.Subscription{}, err
	}
	f.nextID++
	s.ID = fmt.Sprintf("sub%d", f.nextID)
	f.subs[s.ID] = s
	return s, nil
}

func (f *fakeGraph) RenewSubscription(_ context.Context, id string, exp time.Time) (graph.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("renew"); err != nil {
		return graph.Subscription{}, err
	}
	s, ok := f.subs[id]
	if !ok {
		return s, &graph.Error{Status: 404}
	}
	s.ExpirationDateTime = exp
	f.subs[id] = s
	return s, nil
}

func (f *fakeGraph) RetargetSubscription(_ context.Context, id, n, l string, exp time.Time) (graph.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("retarget"); err != nil {
		return graph.Subscription{}, err
	}
	s, ok := f.subs[id]
	if !ok {
		return s, &graph.Error{Status: 404}
	}
	s.NotificationURL, s.LifecycleNotificationURL, s.ExpirationDateTime = n, l, exp
	f.subs[id] = s
	return s, nil
}

func (f *fakeGraph) DeleteSubscription(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = f.record("delete")
	delete(f.subs, id)
	return nil
}

func (f *fakeGraph) only() (graph.Subscription, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.subs {
		return s, true
	}
	return graph.Subscription{}, false
}

func (f *fakeGraph) count(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == call {
			n++
		}
	}
	return n
}

// deliver POSTs like Graph does. lifecycle selects the lifecycle URL.
func (f *fakeGraph) deliver(t *testing.T, s graph.Subscription, n graph.ChangeNotification, lifecycle bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"value": []graph.ChangeNotification{n}})
	target := s.NotificationURL
	if lifecycle {
		target = s.LifecycleNotificationURL
	}
	resp, err := http.Post(target, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("relay answered %d", resp.StatusCode)
	}
}

type event struct{ kind, chat, msg, change string }

type recHandler struct{ ch chan event }

func (h recHandler) OnMessage(chatID, messageID, changeType string) {
	h.ch <- event{"message", chatID, messageID, changeType}
}
func (h recHandler) OnResync() { h.ch <- event{kind: "resync"} }
func (h recHandler) OnState(live bool, _ error) {
	if live {
		h.ch <- event{kind: "live"}
	} else {
		h.ch <- event{kind: "down"}
	}
}

func expect(t *testing.T, ch <-chan event, kind string) event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.kind == kind {
				return e
			}
		case <-timeout:
			t.Fatalf("timeout waiting for %q", kind)
		}
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timeout: %s", what)
}

type harness struct {
	relay  *relay.Server
	http   *httptest.Server
	graph  *fakeGraph
	events chan event
	cancel context.CancelFunc
	done   chan struct{}
	mgr    *Manager
}

func start(t *testing.T, cfg Config) *harness {
	t.Helper()
	rs := relay.NewServer(relay.DefaultConfig(), nil)
	hs := httptest.NewServer(rs.Handler())
	t.Cleanup(func() { rs.Shutdown(); hs.Close() })

	h := &harness{relay: rs, http: hs, graph: newFakeGraph(), events: make(chan event, 64), done: make(chan struct{})}
	cfg.RelayURL = hs.URL
	cfg.UserID = "user-1"
	h.mgr = New(cfg, h.graph, recHandler{h.events})

	rc := relayc.New(hs.URL, "test")
	rc.MinBackoff, rc.MaxBackoff = 10*time.Millisecond, 50*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	relayEvents := make(chan relayc.Event, 16)
	go func() { _ = rc.Run(ctx, relayEvents) }()
	go func() { h.mgr.Run(ctx, relayEvents); close(h.done) }()
	t.Cleanup(func() { cancel(); <-h.done })
	return h
}

func msgNotification(s graph.Subscription, clientState string) graph.ChangeNotification {
	return graph.ChangeNotification{
		SubscriptionID: s.ID, ClientState: clientState, ChangeType: "created", TenantID: "t",
		Resource: "chats('19:abc@thread.v2')/messages('1700000000000')",
	}
}

// The M6 core path: subscription created against the relay, Graph webhook
// travels Graph → relay → WebSocket → manager → handler.
func TestEndToEndNotification(t *testing.T) {
	h := start(t, Config{})
	expect(t, h.events, "live")

	s, ok := h.graph.only()
	if !ok {
		t.Fatal("no subscription created")
	}
	if s.Resource != "/users/user-1/chats/getAllMessages" || s.ChangeType != ChangeTypes || s.IncludeResourceData {
		t.Fatalf("subscription %+v", s)
	}
	if !strings.Contains(s.NotificationURL, "/hook?r=") || !strings.Contains(s.LifecycleNotificationURL, "/lifecycle?r=") || len(s.ClientState) < 32 {
		t.Fatalf("urls/clientState %+v", s)
	}
	if d := time.Until(s.ExpirationDateTime); d < 59*time.Minute || d > 61*time.Minute {
		t.Fatalf("expiration in %v", d)
	}

	h.graph.deliver(t, s, msgNotification(s, s.ClientState), false)
	e := expect(t, h.events, "message")
	if e.chat != "19:abc@thread.v2" || e.msg != "1700000000000" || e.change != "created" {
		t.Fatalf("%+v", e)
	}
}

func TestForgedClientStateAndForeignSubscriptionIgnored(t *testing.T) {
	h := start(t, Config{})
	expect(t, h.events, "live")
	s, _ := h.graph.only()

	h.graph.deliver(t, s, msgNotification(s, "forged"), false)
	foreign := msgNotification(s, s.ClientState)
	foreign.SubscriptionID = "other"
	h.graph.deliver(t, s, foreign, false)
	h.graph.deliver(t, s, msgNotification(s, s.ClientState), false) // marker

	e := expect(t, h.events, "message")
	select {
	case extra := <-h.events:
		t.Fatalf("unexpected extra event %+v", extra)
	case <-time.After(100 * time.Millisecond):
	}
	_ = e
}

func TestRelayRestartRetargetsAndResyncs(t *testing.T) {
	h := start(t, Config{})
	expect(t, h.events, "live")
	before, _ := h.graph.only()

	h.relay.Shutdown() // all clients reconnect with new tokens
	expect(t, h.events, "down")
	expect(t, h.events, "resync")
	waitFor(t, "retarget", func() bool { return h.graph.count("retarget") == 1 })

	after, _ := h.graph.only()
	if after.ID != before.ID || after.NotificationURL == before.NotificationURL {
		t.Fatalf("subscription not re-pointed: %+v -> %+v", before, after)
	}
	h.graph.deliver(t, after, msgNotification(after, after.ClientState), false)
	expect(t, h.events, "message")
}

func TestRetargetFailureRecreates(t *testing.T) {
	h := start(t, Config{})
	expect(t, h.events, "live")
	h.graph.mu.Lock()
	h.graph.failNext["retarget"] = &graph.Error{Status: 400, Code: "InvalidRequest"}
	h.graph.mu.Unlock()

	h.relay.Shutdown()
	expect(t, h.events, "resync")
	waitFor(t, "recreate", func() bool { return h.graph.count("create") == 2 && h.graph.count("delete") == 1 })
	s, _ := h.graph.only()
	h.graph.deliver(t, s, msgNotification(s, s.ClientState), false)
	expect(t, h.events, "message")
}

func TestLifecycleEvents(t *testing.T) {
	h := start(t, Config{})
	expect(t, h.events, "live")
	s, _ := h.graph.only()
	lc := func(ev string) graph.ChangeNotification {
		return graph.ChangeNotification{SubscriptionID: s.ID, ClientState: s.ClientState, LifecycleEvent: ev}
	}

	h.graph.deliver(t, s, lc(graph.LifecycleMissed), true)
	expect(t, h.events, "resync")

	h.graph.deliver(t, s, lc(graph.LifecycleReauthorizationRequired), true)
	waitFor(t, "renew", func() bool { return h.graph.count("renew") == 1 })

	// Graph removed the subscription → a new one is created + resync.
	h.graph.mu.Lock()
	delete(h.graph.subs, s.ID)
	h.graph.mu.Unlock()
	h.graph.deliver(t, s, lc(graph.LifecycleSubscriptionRemoved), true)
	expect(t, h.events, "resync")
	waitFor(t, "recreated", func() bool { _, ok := h.graph.only(); return ok && h.graph.count("create") == 2 })
}

func TestRenewBeforeExpiry(t *testing.T) {
	h := start(t, Config{Lifetime: 400 * time.Millisecond, RenewBefore: 300 * time.Millisecond})
	expect(t, h.events, "live")
	waitFor(t, "two renewals", func() bool { return h.graph.count("renew") >= 2 })
}

func TestCreateFailureRetries(t *testing.T) {
	cfg := Config{RetryAfter: 50 * time.Millisecond}
	rs := relay.NewServer(relay.DefaultConfig(), nil)
	hs := httptest.NewServer(rs.Handler())
	defer func() { rs.Shutdown(); hs.Close() }()
	fg := newFakeGraph()
	fg.failNext["create"] = &graph.Error{Status: 403, Code: "Forbidden"}
	events := make(chan event, 64)
	cfg.RelayURL, cfg.UserID = hs.URL, "u"
	m := New(cfg, fg, recHandler{events})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	re := make(chan relayc.Event, 4)
	rc := relayc.New(hs.URL, "t")
	go func() { _ = rc.Run(ctx, re) }()
	go m.Run(ctx, re)
	expect(t, events, "down")
	expect(t, events, "live")
	if fg.count("create") != 2 {
		t.Fatalf("creates %d", fg.count("create"))
	}
}

func TestShutdownDeletesSubscription(t *testing.T) {
	h := start(t, Config{})
	expect(t, h.events, "live")
	h.cancel()
	<-h.done
	if _, ok := h.graph.only(); ok || h.graph.count("delete") != 1 {
		t.Fatal("subscription not deleted on shutdown")
	}
}
