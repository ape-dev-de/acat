// Package relayc is the client side of the acat-relay WebSocket: it
// connects, performs the hello/welcome handshake, delivers pushed Graph
// payloads and reconnects with backoff. Every (re)connect yields a new
// routing token, which the subscription manager must re-point Graph to.
package relayc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/ape-dev-de/acat/pkg/protocol"
)

// EventKind describes an Event.
type EventKind int

// Event kinds.
const (
	Connected    EventKind = iota // new routing token available
	Disconnected                  // connection lost; reconnect pending
	Notification                  // Graph change notification payload
	Lifecycle                     // Graph lifecycle notification payload
	Fatal                         // unrecoverable (e.g. version mismatch); Run returns
)

// Event is emitted by Run.
type Event struct {
	Kind         EventKind
	RoutingToken string          // Connected
	Features     []string        // Connected
	Payload      json.RawMessage // Notification, Lifecycle
	Err          error           // Disconnected, Fatal
}

// ErrUnsupportedVersion means relay and client share no protocol version.
var ErrUnsupportedVersion = errors.New("relayc: relay does not support this client version")

// Client connects to one relay.
type Client struct {
	URL        string // ws(s)://host[/ws]
	ClientName string // e.g. "acat/0.1.0"
	MinBackoff time.Duration
	MaxBackoff time.Duration
	// StableAfter resets the backoff once a connection lived this long.
	StableAfter time.Duration
}

// New returns a client with default backoff (1 s … 60 s).
func New(relayURL, clientName string) *Client {
	return &Client{URL: relayURL, ClientName: clientName, MinBackoff: time.Second, MaxBackoff: time.Minute, StableAfter: time.Minute}
}

// WSURL returns the WebSocket endpoint for a configured relay URL.
func WSURL(relayURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(relayURL))
	if err != nil {
		return "", fmt.Errorf("relayc: relay URL: %w", err)
	}
	switch u.Scheme {
	case "wss", "ws":
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("relayc: relay URL must start with wss:// or https://, got %q", relayURL)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/ws"
	}
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

// HookURLs returns the Graph notification and lifecycle URLs for token.
func HookURLs(relayURL, token string) (notification, lifecycle string, err error) {
	u, err := url.Parse(strings.TrimSpace(relayURL))
	if err != nil {
		return "", "", fmt.Errorf("relayc: relay URL: %w", err)
	}
	switch u.Scheme {
	case "wss", "https":
		u.Scheme = "https"
	case "ws", "http":
		u.Scheme = "http" // tests only; Graph requires https
	default:
		return "", "", fmt.Errorf("relayc: bad relay URL scheme %q", u.Scheme)
	}
	q := url.Values{"r": {token}}.Encode()
	base := u.Scheme + "://" + u.Host
	return base + "/hook?" + q, base + "/lifecycle?" + q, nil
}

// Run connects and reconnects until ctx ends or a fatal error occurs. Events
// are sent to out; Run blocks while out is full (the consumer must keep up).
func (c *Client) Run(ctx context.Context, out chan<- Event) error {
	wsURL, err := WSURL(c.URL)
	if err != nil {
		return err
	}
	backoff := c.MinBackoff
	for {
		started := time.Now()
		err := c.session(ctx, wsURL, out)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ErrUnsupportedVersion) {
			send(ctx, out, Event{Kind: Fatal, Err: err})
			return err
		}
		send(ctx, out, Event{Kind: Disconnected, Err: err})

		if time.Since(started) >= c.StableAfter {
			backoff = c.MinBackoff
		}
		wait := backoff/2 + time.Duration(rand.Int64N(int64(backoff)/2+1)) //nolint:gosec // jitter
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		backoff = min(backoff*2, c.MaxBackoff)
	}
}

func send(ctx context.Context, out chan<- Event, e Event) {
	select {
	case out <- e:
	case <-ctx.Done():
	}
}

// session runs one connection until it ends.
func (c *Client) session(ctx context.Context, wsURL string, out chan<- Event) error {
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	conn, _, err := websocket.Dial(dctx, wsURL, nil) //nolint:bodyclose // coder/websocket: response body needs no closing
	cancel()
	if err != nil {
		return fmt.Errorf("relayc: dial: %w", err)
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(1 << 20)

	hctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := wsjson.Write(hctx, conn, protocol.Hello{Type: protocol.TypeHello, Proto: protocol.RelayVersion, Client: c.ClientName}); err != nil {
		return fmt.Errorf("relayc: hello: %w", err)
	}
	var raw json.RawMessage
	if err := wsjson.Read(hctx, conn, &raw); err != nil {
		return fmt.Errorf("relayc: welcome: %w", err)
	}
	var env protocol.Envelope
	_ = json.Unmarshal(raw, &env)
	switch env.Type {
	case protocol.TypeWelcome:
	case protocol.TypeError:
		var pe protocol.Error
		_ = json.Unmarshal(raw, &pe)
		if pe.Code == protocol.ErrCodeUnsupportedVersion {
			return fmt.Errorf("%w: %s", ErrUnsupportedVersion, pe.Message)
		}
		return fmt.Errorf("relayc: relay refused: %s %s", pe.Code, pe.Message)
	default:
		return fmt.Errorf("relayc: unexpected first frame %q", env.Type)
	}
	var w protocol.Welcome
	if err := json.Unmarshal(raw, &w); err != nil || w.RoutingToken == "" {
		return errors.New("relayc: invalid welcome")
	}
	send(ctx, out, Event{Kind: Connected, RoutingToken: w.RoutingToken, Features: w.Features})

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("relayc: read: %w", err)
		}
		var push protocol.Push
		if json.Unmarshal(data, &push) != nil {
			continue // ignore garbage; forward-compatible with unknown frames
		}
		switch push.Type {
		case protocol.TypeNotification:
			send(ctx, out, Event{Kind: Notification, Payload: push.Payload})
		case protocol.TypeLifecycle:
			send(ctx, out, Event{Kind: Lifecycle, Payload: push.Payload})
		}
	}
}
