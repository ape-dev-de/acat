// Package acs obtains Azure Communication Services access tokens for a
// Teams user by exchanging an Entra token (ACS scopes) through acat-relay.
package acs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Token is an ACS access token.
type Token struct {
	Value     string    `json:"token"`
	ExpiresOn time.Time `json:"expiresOn"`
}

// ErrForbidden means ACS or the relay refused this user/tenant.
var ErrForbidden = errors.New("acs: token exchange refused (tenant/app not allowed or consent missing)")

// ErrNotSupported means the relay has no ACS exchange configured.
var ErrNotSupported = errors.New("acs: relay does not offer token exchange")

// Exchanger talks to the relay's /acs/token endpoint.
type Exchanger struct {
	Endpoint string // https://relay.example.org/acs/token
	HTTP     *http.Client
}

// NewExchanger builds the endpoint from a configured relay URL
// (wss://, https://, ws:// or http://).
func NewExchanger(relayURL string) (*Exchanger, error) {
	u, err := url.Parse(strings.TrimSpace(relayURL))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("acs: bad relay URL %q", relayURL)
	}
	switch u.Scheme {
	case "wss", "https":
		u.Scheme = "https"
	case "ws", "http":
		u.Scheme = "http"
	default:
		return nil, fmt.Errorf("acs: bad relay URL scheme %q", u.Scheme)
	}
	return &Exchanger{Endpoint: u.Scheme + "://" + u.Host + "/acs/token", HTTP: &http.Client{Timeout: 30 * time.Second}}, nil
}

// Exchange trades an Entra token for an ACS token.
func (e *Exchanger) Exchange(ctx context.Context, entraToken string) (Token, error) {
	body, _ := json.Marshal(map[string]string{"token": entraToken})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("acs: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden:
		return Token{}, ErrForbidden
	case http.StatusNotImplemented, http.StatusNotFound:
		return Token{}, ErrNotSupported
	default:
		return Token{}, fmt.Errorf("acs: relay answered HTTP %d", resp.StatusCode)
	}
	var t Token
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&t); err != nil || t.Value == "" {
		return Token{}, errors.New("acs: bad response from relay")
	}
	return t, nil
}

// RefreshBefore is how long before expiry a token is renewed.
const RefreshBefore = 10 * time.Minute

// TokenSource caches an ACS token and renews it before it expires.
type TokenSource struct {
	ex    *Exchanger
	entra func(ctx context.Context) (string, error) // Entra token with ACS scopes
	now   func() time.Time

	mu  sync.Mutex
	cur Token
}

// NewTokenSource combines an exchanger with an Entra token provider.
func NewTokenSource(ex *Exchanger, entra func(ctx context.Context) (string, error)) *TokenSource {
	return &TokenSource{ex: ex, entra: entra, now: time.Now}
}

// Token returns a valid ACS token.
func (s *TokenSource) Token(ctx context.Context) (Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur.Value != "" && s.now().Before(s.cur.ExpiresOn.Add(-RefreshBefore)) {
		return s.cur, nil
	}
	et, err := s.entra(ctx)
	if err != nil {
		return Token{}, err
	}
	t, err := s.ex.Exchange(ctx, et)
	if err != nil {
		return Token{}, err
	}
	s.cur = t
	return t, nil
}

// Invalidate forces the next Token call to exchange again.
func (s *TokenSource) Invalidate() {
	s.mu.Lock()
	s.cur = Token{}
	s.mu.Unlock()
}
