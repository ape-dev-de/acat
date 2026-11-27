package graph

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient returns a client against h with instant, recorded sleeps.
func newTestClient(t *testing.T, h http.Handler) (*Client, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(TokenFunc(func(context.Context) (string, error) { return "tok", nil }))
	c.BaseURL = srv.URL + "/v1.0"
	var slept []time.Duration
	c.sleep = func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		return ctx.Err()
	}
	return c, &slept
}

func TestAuthHeaderAndQuery(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("auth header %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/v1.0/me/chats" {
			t.Errorf("path %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("$expand"); got != "members,lastMessagePreview" {
			t.Errorf("$expand = %q", got)
		}
		if !strings.Contains(r.URL.RawQuery, "$orderby=") {
			t.Errorf("raw query not OData-friendly: %s", r.URL.RawQuery)
		}
		fmt.Fprint(w, `{"value":[]}`)
	}))
	if _, err := c.ListChats(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
}

func TestPagingFollowsNextLink(t *testing.T) {
	var srvURL string
	var calls atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Query().Get("$skiptoken") {
		case "":
			fmt.Fprintf(w, `{"value":[{"id":"a","lastUpdatedDateTime":"2026-01-03T00:00:00Z"}],"@odata.nextLink":"%s/v1.0/me/chats?$skiptoken=p2"}`, srvURL)
		case "p2":
			fmt.Fprintf(w, `{"value":[{"id":"b","lastUpdatedDateTime":"2026-01-02T00:00:00Z"}],"@odata.nextLink":"%s/v1.0/me/chats?$skiptoken=p3"}`, srvURL)
		case "p3":
			fmt.Fprint(w, `{"value":[{"id":"c","lastUpdatedDateTime":"2026-01-01T00:00:00Z"}]}`)
		}
	}))
	srvURL = strings.TrimSuffix(c.BaseURL, "/v1.0")

	chats, err := c.ListChats(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 3 || chats[0].ID != "a" || chats[2].ID != "c" {
		t.Fatalf("got %+v", chats)
	}

	calls.Store(0)
	chats, err = c.ListChats(context.Background(), 2)
	if err != nil || len(chats) != 2 || calls.Load() != 2 {
		t.Fatalf("limit: %d chats, %d calls, err %v", len(chats), calls.Load(), err)
	}
}

func TestRefusesForeignNextLink(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"value":[{"id":"a"}],"@odata.nextLink":"https://evil.example/steal"}`)
	}))
	_, err := c.ListChats(context.Background(), 0)
	if err == nil || !strings.Contains(err.Error(), "foreign host") {
		t.Fatalf("want foreign host error, got %v", err)
	}
}

func TestRetryOn429WithRetryAfter(t *testing.T) {
	var calls atomic.Int32
	c, slept := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) <= 2 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"code":"TooManyRequests","message":"slow down"}}`)
			return
		}
		fmt.Fprint(w, `{"id":"me","displayName":"Me"}`)
	}))
	u, err := c.Me(context.Background())
	if err != nil || u.ID != "me" {
		t.Fatalf("got %+v, %v", u, err)
	}
	if len(*slept) != 2 || (*slept)[0] != 7*time.Second {
		t.Fatalf("slept %v, want two 7s waits", *slept)
	}
}

func TestRetryOn503WithBackoffThenGiveUp(t *testing.T) {
	var calls atomic.Int32
	c, slept := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	c.MaxRetries = 3
	_, err := c.Me(context.Background())
	if !IsStatus(err, http.StatusServiceUnavailable) {
		t.Fatalf("want 503 error, got %v", err)
	}
	if calls.Load() != 4 || len(*slept) != 3 {
		t.Fatalf("calls %d, sleeps %d", calls.Load(), len(*slept))
	}
	for i, d := range *slept {
		base := time.Second << i
		if d < base/2 || d >= base+base/2 {
			t.Fatalf("sleep %d = %v outside jitter window for %v", i, d, base)
		}
	}
}

func TestRetryAfterIsCapped(t *testing.T) {
	var calls atomic.Int32
	c, slept := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{}`)
	}))
	c.MaxBackoff = 30 * time.Second
	if _, err := c.Me(context.Background()); err != nil {
		t.Fatal(err)
	}
	if (*slept)[0] != 30*time.Second {
		t.Fatalf("slept %v", *slept)
	}
}

func TestNoRetryOnClientErrors(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"code":"Forbidden","message":"Missing scope permissions"}}`)
	}))
	_, err := c.Me(context.Background())
	var ge *Error
	if !errors.As(err, &ge) || ge.Status != 403 || ge.Code != "Forbidden" || ge.Message != "Missing scope permissions" {
		t.Fatalf("got %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls %d", calls.Load())
	}
}

func TestContextCancelStopsRetries(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	c.sleep = func(context.Context, time.Duration) error { cancel(); return context.Canceled }
	if _, err := c.Me(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestTokenErrorIsReturned(t *testing.T) {
	c := New(TokenFunc(func(context.Context) (string, error) { return "", errors.New("no account") }))
	c.MaxRetries = 0
	if _, err := c.Me(context.Background()); err == nil || !strings.Contains(err.Error(), "no account") {
		t.Fatalf("got %v", err)
	}
}

func TestRequestTimeoutPerAttempt(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			<-r.Context().Done() // hang until the client gives up
			return
		}
		fmt.Fprint(w, `{"id":"me"}`)
	}))
	c.RequestTimeout = 50 * time.Millisecond
	u, err := c.Me(context.Background())
	if err != nil || u.ID != "me" {
		t.Fatalf("got %+v, %v", u, err)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"":                              0,
		"5":                             5 * time.Second,
		"-1":                            0,
		"garbage":                       0,
		"Thu, 01 Jan 2026 12:00:10 GMT": 10 * time.Second,
		"Thu, 01 Jan 2026 11:00:00 GMT": 0,
	}
	for in, want := range cases {
		if got := retryAfter(in, now); got != want {
			t.Errorf("retryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestMessagesNewestFirstAndOlderPages(t *testing.T) {
	var srvURL string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1.0/chats/19:abc@thread.v2/messages") {
			t.Errorf("path %q", r.URL.Path)
		}
		if r.URL.Query().Get("$skiptoken") == "older" {
			fmt.Fprint(w, `{"value":[{"id":"1","messageType":"message","body":{"contentType":"text","content":"first"}}]}`)
			return
		}
		if r.URL.Query().Get("$top") != "50" || r.URL.Query().Get("$orderby") != "createdDateTime desc" {
			t.Errorf("query %q", r.URL.RawQuery)
		}
		fmt.Fprintf(w, `{"value":[{"id":"3"},{"id":"2","deletedDateTime":"2026-01-01T00:00:00Z"}],"@odata.nextLink":"%s/v1.0/chats/19:abc@thread.v2/messages?$skiptoken=older"}`, srvURL)
	}))
	srvURL = strings.TrimSuffix(c.BaseURL, "/v1.0")

	p, err := c.ListMessages(context.Background(), "19:abc@thread.v2", 0)
	if err != nil || len(p.Items) != 2 || p.NextLink == "" || !p.Items[1].IsDeleted() {
		t.Fatalf("page 1: %+v %v", p, err)
	}
	p, err = c.MoreMessages(context.Background(), p.NextLink)
	if err != nil || len(p.Items) != 1 || p.NextLink != "" || p.Items[0].Body.Content != "first" {
		t.Fatalf("page 2: %+v %v", p, err)
	}
}
