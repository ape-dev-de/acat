package acs

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewExchangerURL(t *testing.T) {
	for in, want := range map[string]string{
		"wss://relay.example.org":        "https://relay.example.org/acs/token",
		"https://relay.example.org/ws?x": "https://relay.example.org/acs/token",
		"ws://127.0.0.1:8080":            "http://127.0.0.1:8080/acs/token",
	} {
		e, err := NewExchanger(in)
		if err != nil || e.Endpoint != want {
			t.Errorf("%s: %v %v", in, e, err)
		}
	}
	if _, err := NewExchanger("ftp://x"); err == nil {
		t.Error("want error")
	}
}

func TestTokenSourceCachesAndRefreshes(t *testing.T) {
	var exchanges atomic.Int32
	expires := time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"token":"entra-`) {
			t.Errorf("body %s", b)
		}
		exchanges.Add(1)
		_, _ = io.WriteString(w, `{"token":"acs-`+strconv.Itoa(int(exchanges.Load()))+`","expiresOn":"`+expires.Format(time.RFC3339)+`"}`)
	}))
	defer srv.Close()
	ex, _ := NewExchanger(srv.URL)
	var entraCalls int
	ts := NewTokenSource(ex, func(context.Context) (string, error) { entraCalls++; return "entra-x", nil })
	now := expires.Add(-time.Hour)
	ts.now = func() time.Time { return now }

	t1, err := ts.Token(context.Background())
	if err != nil || t1.Value != "acs-1" {
		t.Fatalf("%+v %v", t1, err)
	}
	t2, _ := ts.Token(context.Background())
	if t2.Value != "acs-1" || exchanges.Load() != 1 {
		t.Fatal("should be cached")
	}
	now = expires.Add(-5 * time.Minute) // within refresh window
	t3, _ := ts.Token(context.Background())
	if t3.Value != "acs-2" || entraCalls != 2 {
		t.Fatalf("should refresh: %+v", t3)
	}
	ts.Invalidate()
	if t4, _ := ts.Token(context.Background()); t4.Value != "acs-3" {
		t.Fatalf("invalidate: %+v", t4)
	}
}

func TestExchangeErrors(t *testing.T) {
	for status, want := range map[int]error{http.StatusForbidden: ErrForbidden, http.StatusNotImplemented: ErrNotSupported} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		ex, _ := NewExchanger(srv.URL)
		if _, err := ex.Exchange(context.Background(), "x"); !errors.Is(err, want) {
			t.Errorf("%d: %v", status, err)
		}
		srv.Close()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{}`) }))
	defer srv.Close()
	ex, _ := NewExchanger(srv.URL)
	if _, err := ex.Exchange(context.Background(), "x"); err == nil {
		t.Error("empty token must fail")
	}
	ts := NewTokenSource(ex, func(context.Context) (string, error) { return "", errors.New("no consent") })
	if _, err := ts.Token(context.Background()); err == nil || !strings.Contains(err.Error(), "no consent") {
		t.Errorf("entra error: %v", err)
	}
}
