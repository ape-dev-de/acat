package relay

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"

	"golang.org/x/time/rate"
)

// tokenBytes is the routing token entropy: 192 bit (plan requires >= 128).
const tokenBytes = 24

func newRoutingToken() string {
	b := make([]byte, tokenBytes)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error (Go >= 1.24)
	return base64.RawURLEncoding.EncodeToString(b)
}

// client is one WebSocket connection. Its routing token exists only here,
// in memory, for the lifetime of the connection.
type client struct {
	token   string
	ip      string
	send    chan []byte
	done    chan struct{}
	once    sync.Once
	hookLim *rate.Limiter
	// closeReason is set before done is closed; read only after <-done.
	closeReason string
}

func newClient(token, ip string, buf int, hookRate float64, hookBurst int) *client {
	return &client{
		token:   token,
		ip:      ip,
		send:    make(chan []byte, buf),
		done:    make(chan struct{}),
		hookLim: rate.NewLimiter(rate.Limit(hookRate), hookBurst),
	}
}

// kick asks the connection's handler to close it.
func (c *client) kick(reason string) {
	c.once.Do(func() {
		c.closeReason = reason
		close(c.done)
	})
}

var errTooManyConns = errors.New("too many connections from this address")

// hub maps routing tokens to live connections. It is the relay's only state.
type hub struct {
	mu       sync.Mutex
	byToken  map[string]*client
	perIP    map[string]int
	maxPerIP int
}

func newHub(maxPerIP int) *hub {
	return &hub{byToken: map[string]*client{}, perIP: map[string]int{}, maxPerIP: maxPerIP}
}

func (h *hub) register(c *client) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.perIP[c.ip] >= h.maxPerIP {
		return errTooManyConns
	}
	h.perIP[c.ip]++
	h.byToken[c.token] = c
	return nil
}

func (h *hub) unregister(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byToken[c.token] != c {
		return
	}
	delete(h.byToken, c.token)
	if h.perIP[c.ip]--; h.perIP[c.ip] <= 0 {
		delete(h.perIP, c.ip)
	}
}

func (h *hub) get(token string) *client {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.byToken[token]
}

func (h *hub) len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.byToken)
}

// kickAll closes every connection (graceful shutdown → clients reconnect).
func (h *hub) kickAll(reason string) {
	h.mu.Lock()
	cs := make([]*client, 0, len(h.byToken))
	for _, c := range h.byToken {
		cs = append(cs, c)
	}
	h.mu.Unlock()
	for _, c := range cs {
		c.kick(reason)
	}
}
