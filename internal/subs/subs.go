// Package subs owns the Graph change-notification subscription of one
// account. It reacts to relay events (new routing token → re-point the
// subscription), renews before expiry, handles lifecycle events, verifies
// clientState, and reports chat-message changes to a Handler.
package subs

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/ape-dev-de/acat/internal/graph"
	"github.com/ape-dev-de/acat/internal/relayc"
)

// GraphAPI is the subset of graph.Client used here.
type GraphAPI interface {
	CreateSubscription(ctx context.Context, s graph.Subscription) (graph.Subscription, error)
	RenewSubscription(ctx context.Context, id string, expires time.Time) (graph.Subscription, error)
	RetargetSubscription(ctx context.Context, id, notificationURL, lifecycleURL string, expires time.Time) (graph.Subscription, error)
	DeleteSubscription(ctx context.Context, id string) error
}

// Handler receives results. Implementations must not block for long; do
// Graph fetches in their own goroutines.
type Handler interface {
	// OnMessage: a chat message was created, updated or deleted.
	OnMessage(chatID, messageID, changeType string)
	// OnResync: notifications may have been lost (reconnect, "missed",
	// subscription re-created); reload chats and open threads.
	OnResync()
	// OnState reports whether live updates currently work.
	OnState(live bool, err error)
}

// Config configures a Manager.
type Config struct {
	RelayURL    string        // as configured, e.g. wss://relay.example.org
	UserID      string        // signed-in user's object ID
	Lifetime    time.Duration // requested subscription lifetime (default 60 min)
	RenewBefore time.Duration // renew this long before expiry (default 15 min)
	RetryAfter  time.Duration // retry delay after a failed create (default 1 min)
}

// ChangeTypes subscribed to.
const ChangeTypes = "created,updated,deleted"

// Manager runs the subscription lifecycle.
type Manager struct {
	cfg         Config
	api         GraphAPI
	h           Handler
	clientState string
	now         func() time.Time

	token    string
	sub      *graph.Subscription
	timer    *time.Timer
	hadToken bool
}

// New creates a Manager. A fresh random clientState is generated per run;
// it never leaves this process except inside the subscription itself.
func New(cfg Config, api GraphAPI, h Handler) *Manager {
	if cfg.Lifetime <= 0 {
		cfg.Lifetime = 60 * time.Minute
	}
	if cfg.RenewBefore <= 0 || cfg.RenewBefore >= cfg.Lifetime {
		cfg.RenewBefore = cfg.Lifetime / 4
	}
	if cfg.RetryAfter <= 0 {
		cfg.RetryAfter = time.Minute
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return &Manager{cfg: cfg, api: api, h: h, clientState: base64.RawURLEncoding.EncodeToString(b), now: time.Now}
}

// Resource is the subscribed Graph resource.
func (m *Manager) Resource() string { return "/users/" + m.cfg.UserID + "/chats/getAllMessages" }

// Run processes relay events until ctx ends, then deletes the subscription.
func (m *Manager) Run(ctx context.Context, events <-chan relayc.Event) {
	m.timer = time.NewTimer(time.Hour)
	m.timer.Stop()
	defer m.cleanup()
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-events:
			m.handleEvent(ctx, e)
		case <-m.timer.C:
			m.renew(ctx)
		}
	}
}

func (m *Manager) handleEvent(ctx context.Context, e relayc.Event) {
	switch e.Kind {
	case relayc.Connected:
		reconnect := m.hadToken
		m.token, m.hadToken = e.RoutingToken, true
		m.ensure(ctx)
		if reconnect {
			m.h.OnResync()
		}
	case relayc.Disconnected:
		m.token = ""
		m.h.OnState(false, e.Err)
	case relayc.Fatal:
		m.token = ""
		m.h.OnState(false, e.Err)
	case relayc.Notification:
		m.handlePayload(ctx, e.Payload, false)
	case relayc.Lifecycle:
		m.handlePayload(ctx, e.Payload, true)
	}
}

// ensure creates or re-points the subscription to the current token.
func (m *Manager) ensure(ctx context.Context) {
	if m.token == "" {
		return
	}
	nURL, lURL, err := relayc.HookURLs(m.cfg.RelayURL, m.token)
	if err != nil {
		m.h.OnState(false, err)
		return
	}
	exp := m.now().Add(m.cfg.Lifetime)

	if m.sub != nil {
		s, err := m.api.RetargetSubscription(ctx, m.sub.ID, nURL, lURL, exp)
		if err == nil {
			m.setSub(s, exp)
			m.h.OnState(true, nil)
			return
		}
		// Gone or not patchable: start over.
		m.deleteQuietly(ctx)
	}

	s, err := m.api.CreateSubscription(ctx, graph.Subscription{
		Resource:                 m.Resource(),
		ChangeType:               ChangeTypes,
		NotificationURL:          nURL,
		LifecycleNotificationURL: lURL,
		ClientState:              m.clientState,
		ExpirationDateTime:       exp,
	})
	if err != nil {
		m.sub = nil
		m.h.OnState(false, fmt.Errorf("subscribe to chat messages: %w", err))
		m.schedule(m.cfg.RetryAfter)
		return
	}
	m.setSub(s, exp)
	m.h.OnState(true, nil)
}

func (m *Manager) setSub(s graph.Subscription, requested time.Time) {
	if s.ExpirationDateTime.IsZero() {
		s.ExpirationDateTime = requested
	}
	if m.sub != nil && s.ID == "" {
		s.ID = m.sub.ID
	}
	m.sub = &s
	m.schedule(time.Until(s.ExpirationDateTime) - m.cfg.RenewBefore)
}

func (m *Manager) schedule(d time.Duration) {
	m.timer.Stop()
	m.timer.Reset(max(d, 0))
}

func (m *Manager) renew(ctx context.Context) {
	if m.sub == nil {
		m.ensure(ctx) // retry a failed create
		return
	}
	exp := m.now().Add(m.cfg.Lifetime)
	s, err := m.api.RenewSubscription(ctx, m.sub.ID, exp)
	if err != nil {
		if graph.IsStatus(err, http.StatusNotFound) {
			m.sub = nil
			m.ensure(ctx)
			m.h.OnResync()
			return
		}
		m.h.OnState(false, fmt.Errorf("renew subscription: %w", err))
		m.schedule(m.cfg.RetryAfter)
		return
	}
	m.setSub(s, exp)
}

func (m *Manager) handlePayload(ctx context.Context, payload []byte, lifecycle bool) {
	ns, err := graph.ParseNotifications(payload)
	if err != nil {
		return
	}
	for _, n := range ns {
		if m.sub == nil || n.SubscriptionID != m.sub.ID {
			continue // stale or foreign subscription
		}
		if subtle.ConstantTimeCompare([]byte(n.ClientState), []byte(m.clientState)) != 1 {
			continue // not from Graph for our subscription
		}
		if lifecycle || n.LifecycleEvent != "" {
			m.handleLifecycle(ctx, n.LifecycleEvent)
			continue
		}
		chatID, msgID, ok := graph.ChatMessageRef(n.Resource)
		if !ok {
			continue
		}
		m.h.OnMessage(chatID, msgID, n.ChangeType)
	}
}

func (m *Manager) handleLifecycle(ctx context.Context, event string) {
	switch event {
	case graph.LifecycleReauthorizationRequired:
		m.renew(ctx)
	case graph.LifecycleSubscriptionRemoved:
		m.sub = nil
		m.ensure(ctx)
		m.h.OnResync()
	case graph.LifecycleMissed:
		m.h.OnResync()
	}
}

func (m *Manager) deleteQuietly(ctx context.Context) {
	if m.sub == nil {
		return
	}
	// Best effort: if this fails, Graph expires the subscription anyway.
	_ = m.api.DeleteSubscription(ctx, m.sub.ID)
	m.sub = nil
}

// cleanup deletes the subscription on shutdown (best effort, 10 s).
func (m *Manager) cleanup() {
	m.timer.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m.deleteQuietly(ctx)
}
