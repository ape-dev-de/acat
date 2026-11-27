// Package graph is a small Microsoft Graph REST client covering exactly
// what acat needs (chats, messages, users, presence, subscriptions).
// It deliberately avoids msgraph-sdk-go to keep binary size and memory low.
package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the Graph v1.0 endpoint.
const DefaultBaseURL = "https://graph.microsoft.com/v1.0"

// TokenSource returns a valid access token for Graph.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// TokenFunc adapts a function to TokenSource.
type TokenFunc func(ctx context.Context) (string, error)

// Token implements TokenSource.
func (f TokenFunc) Token(ctx context.Context) (string, error) { return f(ctx) }

// Client talks to Graph on behalf of one signed-in account.
type Client struct {
	BaseURL    string
	HTTP       *http.Client
	Tokens     TokenSource
	MaxRetries int           // retries on 429/503/504 and transport errors
	MaxBackoff time.Duration // cap for computed and server-sent delays
	// RequestTimeout bounds a single attempt (not the whole call).
	RequestTimeout time.Duration

	sleep func(ctx context.Context, d time.Duration) error // test hook
}

// New returns a Client with production defaults.
func New(tokens TokenSource) *Client {
	return &Client{
		BaseURL:        DefaultBaseURL,
		HTTP:           &http.Client{},
		Tokens:         tokens,
		MaxRetries:     4,
		MaxBackoff:     60 * time.Second,
		RequestTimeout: 30 * time.Second,
	}
}

// Error is a non-2xx Graph response.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("graph: HTTP %d", e.Status)
	}
	return fmt.Sprintf("graph: HTTP %d %s: %s", e.Status, e.Code, e.Message)
}

// IsStatus reports whether err is a Graph error with the given status.
func IsStatus(err error, status int) bool {
	var ge *Error
	return errors.As(err, &ge) && ge.Status == status
}

// request describes one Graph call.
type request struct {
	method  string
	path    string // relative to BaseURL, or an absolute @odata.nextLink
	query   url.Values
	body    any
	headers map[string]string
}

// do executes req with retries and decodes a JSON response into out
// (if out is non-nil and the response has a body).
func (c *Client) do(ctx context.Context, req request, out any) error {
	u, err := c.resolve(req.path, req.query)
	if err != nil {
		return err
	}
	var payload []byte
	if req.body != nil {
		if payload, err = json.Marshal(req.body); err != nil {
			return fmt.Errorf("graph: encode body: %w", err)
		}
	}

	for attempt := 0; ; attempt++ {
		resp, err := c.attempt(ctx, req, u, payload)
		if err != nil {
			if ctx.Err() != nil || attempt >= c.MaxRetries {
				return err
			}
			if err := c.wait(ctx, c.backoff(attempt, 0)); err != nil {
				return err
			}
			continue
		}

		if retryable(resp.StatusCode) && attempt < c.MaxRetries {
			delay := c.backoff(attempt, retryAfter(resp.Header.Get("Retry-After"), time.Now()))
			drain(resp)
			if err := c.wait(ctx, delay); err != nil {
				return err
			}
			continue
		}
		return c.decode(resp, out)
	}
}

func (c *Client) attempt(ctx context.Context, req request, u string, payload []byte) (*http.Response, error) {
	tok, err := c.Tokens.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("graph: token: %w", err)
	}
	// Per-attempt timeout; released when the response body is closed.
	var (
		actx   context.Context
		cancel context.CancelFunc
	)
	if c.RequestTimeout > 0 {
		actx, cancel = context.WithTimeout(ctx, c.RequestTimeout)
	} else {
		actx, cancel = context.WithCancel(ctx)
	}
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	hreq, err := http.NewRequestWithContext(actx, req.method, u, body)
	if err != nil {
		cancel()
		return nil, err
	}
	c.setHeaders(hreq, tok, req, payload != nil)
	resp, err := c.HTTP.Do(hreq)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

func (c *Client) setHeaders(h *http.Request, tok string, req request, hasBody bool) {
	h.Header.Set("Authorization", "Bearer "+tok)
	h.Header.Set("Accept", "application/json")
	if hasBody {
		h.Header.Set("Content-Type", "application/json")
	}
	for k, v := range req.headers {
		h.Header.Set(k, v)
	}
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func (c *Client) decode(resp *http.Response, out any) error {
	defer drain(resp)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		ge := &Error{Status: resp.StatusCode}
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10)); err == nil && json.Unmarshal(b, &envelope) == nil {
			ge.Code, ge.Message = envelope.Error.Code, envelope.Error.Message
		}
		return ge
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("graph: decode response: %w", err)
	}
	return nil
}

func (c *Client) resolve(path string, q url.Values) (string, error) {
	if strings.HasPrefix(path, "https://") || strings.HasPrefix(path, "http://") {
		// @odata.nextLink: absolute, already carries its query. Only follow
		// links that point at our own Graph host.
		base, err := url.Parse(c.BaseURL)
		if err != nil {
			return "", err
		}
		next, err := url.Parse(path)
		if err != nil {
			return "", fmt.Errorf("graph: bad nextLink: %w", err)
		}
		if next.Host != base.Host || next.Scheme != base.Scheme {
			return "", fmt.Errorf("graph: refusing nextLink to foreign host %q", next.Host)
		}
		return path, nil
	}
	u := strings.TrimRight(c.BaseURL, "/") + "/" + strings.TrimLeft(path, "/")
	if len(q) > 0 {
		u += "?" + encodeQuery(q)
	}
	return u, nil
}

// encodeQuery keeps OData's '$' and ',' readable (Graph accepts both forms).
func encodeQuery(q url.Values) string {
	s := q.Encode()
	return strings.NewReplacer("%24", "$", "%2C", ",").Replace(s)
}

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

// retryAfter parses a Retry-After header (delta-seconds or HTTP-date).
func retryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// backoff returns the delay before retry number attempt+1. A server-sent
// Retry-After wins (capped); otherwise exponential with full jitter.
func (c *Client) backoff(attempt int, server time.Duration) time.Duration {
	limit := c.MaxBackoff
	if limit <= 0 {
		limit = time.Minute
	}
	if server > 0 {
		return min(server, limit)
	}
	base := time.Second << min(attempt, 6)
	base = min(base, limit)
	return time.Duration(rand.Int64N(int64(base)) + int64(base)/2) //nolint:gosec // jitter, not security relevant
}

func (c *Client) wait(ctx context.Context, d time.Duration) error {
	if c.sleep != nil {
		return c.sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
}
