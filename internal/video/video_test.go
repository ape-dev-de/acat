package video

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ape-dev-de/acat/pkg/protocol"
)

// TestMain lets the test binary act as a fake helper when
// ACAT_FAKE_HELPER is set (re-exec pattern).
func TestMain(m *testing.M) {
	if mode := os.Getenv("ACAT_FAKE_HELPER"); mode != "" {
		fakeHelper(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeHelper speaks the helper protocol on stdin/stdout.
func fakeHelper(mode string) {
	out := json.NewEncoder(os.Stdout)
	proto := protocol.VideoVersion
	if mode == "badversion" {
		proto = 99
	}
	_ = out.Encode(protocol.VideoEvent{Type: protocol.VideoReady, Proto: proto, Version: "fake"})
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var c protocol.VideoCommand
		_ = json.Unmarshal(sc.Bytes(), &c)
		switch c.Type {
		case protocol.VideoInit:
			_ = out.Encode(protocol.VideoEvent{Type: protocol.VideoInitialized, Message: c.Token})
			if mode == "expire" {
				_ = out.Encode(protocol.VideoEvent{Type: protocol.VideoTokenExpired})
			}
		case protocol.VideoRefreshToken:
			_ = out.Encode(protocol.VideoEvent{Type: protocol.VideoInitialized, Message: "refreshed:" + c.Token})
		case protocol.VideoCall:
			_ = out.Encode(protocol.VideoEvent{Type: protocol.VideoCallState, CallID: "c1", State: protocol.CallConnecting, Message: c.Participants[0].TeamsUserID})
		case "crash":
			os.Exit(3)
		case protocol.VideoQuit:
			return
		}
	}
}

func newSup(t *testing.T, mode string, tokens *atomic.Int32) *Supervisor {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return New(Config{
		Command: exe,
		Args:    []string{"-test.run=^$"},
		Env:     []string{"ACAT_FAKE_HELPER=" + mode},
		Token: func(context.Context) (string, error) {
			return "tok" + strconv.Itoa(int(tokens.Add(1))), nil
		},
		MinBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond,
	})
}

func next(t *testing.T, s *Supervisor, typ string) protocol.VideoEvent {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case e := <-s.Events():
			if e.Type == typ {
				return e
			}
		case <-timeout:
			t.Fatalf("timeout waiting for %q", typ)
		}
	}
}

func TestInitCallAndRestartAfterCrash(t *testing.T) {
	var tokens atomic.Int32
	s := newSup(t, "ok", &tokens)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	if e := next(t, s, protocol.VideoInitialized); e.Message != "tok1" {
		t.Fatalf("init token %q", e.Message)
	}
	if err := s.Send(protocol.VideoCommand{Type: protocol.VideoCall, Participants: []protocol.Participant{{TeamsUserID: "oid-jane"}}}); err != nil {
		t.Fatal(err)
	}
	if e := next(t, s, protocol.VideoCallState); e.State != protocol.CallConnecting || e.Message != "oid-jane" {
		t.Fatalf("%+v", e)
	}

	_ = s.Send(protocol.VideoCommand{Type: "crash"})
	next(t, s, protocol.VideoError) // "helper stopped"
	if e := next(t, s, protocol.VideoInitialized); e.Message != "tok2" {
		t.Fatalf("after restart: %q", e.Message)
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run: %v", err)
	}
	if err := s.Send(protocol.VideoCommand{Type: protocol.VideoHide}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("send after stop: %v", err)
	}
}

func TestTokenExpiredIsHandledInternally(t *testing.T) {
	var tokens atomic.Int32
	s := newSup(t, "expire", &tokens)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()
	next(t, s, protocol.VideoInitialized)
	if e := next(t, s, protocol.VideoInitialized); e.Message != "refreshed:tok2" {
		t.Fatalf("%q", e.Message)
	}
}

func TestVersionMismatchStops(t *testing.T) {
	var tokens atomic.Int32
	s := newSup(t, "badversion", &tokens)
	err := s.Run(context.Background())
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("got %v", err)
	}
	if tokens.Load() != 0 {
		t.Fatal("token must not be sent to a mismatched helper")
	}
}

func TestMissingHelperBinary(t *testing.T) {
	s := New(Config{Command: "/nonexistent/acat-video", MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	go func() { _ = s.Run(ctx) }()
	e := next(t, s, protocol.VideoError)
	if e.Message == "" {
		t.Fatal("expected error message")
	}
}
