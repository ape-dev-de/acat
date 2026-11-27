package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/ape-dev-de/acat/internal/acs"
	"github.com/ape-dev-de/acat/internal/auth"
	"github.com/ape-dev-de/acat/internal/ui"
	"github.com/ape-dev-de/acat/internal/video"
	"github.com/ape-dev-de/acat/pkg/protocol"
)

// helperCommand finds the video helper:
//   - ACAT_VIDEO_HELPER (+ ACAT_VIDEO_APP for a dev checkout: electron binary + app dir)
//   - next to the acat executable (packaged builds, see M11)
func helperCommand() (string, []string, error) {
	if h := os.Getenv("ACAT_VIDEO_HELPER"); h != "" {
		var args []string
		if app := os.Getenv("ACAT_VIDEO_APP"); app != "" {
			args = []string{app}
		}
		return h, args, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	dir := filepath.Dir(exe)
	name := "acat-video"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	candidates := []string{
		filepath.Join(dir, "acat-video", name),
		filepath.Join(dir, name),
	}
	if runtime.GOOS == "darwin" { // acat.app/Contents/MacOS/acat → Contents/Frameworks/acat-video.app
		candidates = append(candidates, filepath.Join(dir, "..", "Frameworks", "acat-video.app", "Contents", "MacOS", "acat-video"))
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil, nil
		}
	}
	return "", nil, errors.New("video helper not installed (set ACAT_VIDEO_HELPER for development)")
}

// helperCalls adapts video.Supervisor to ui.Calls.
type helperCalls struct {
	sup *video.Supervisor

	mu      sync.Mutex
	handler func(protocol.VideoEvent)
}

func (h *helperCalls) OnEvent(fn func(protocol.VideoEvent)) {
	h.mu.Lock()
	h.handler = fn
	h.mu.Unlock()
}

func (h *helperCalls) pump(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-h.sup.Events():
			h.mu.Lock()
			fn := h.handler
			h.mu.Unlock()
			if fn != nil {
				fn(ev)
			}
		}
	}
}

func (h *helperCalls) send(c protocol.VideoCommand) error { return h.sup.Send(c) }

func (h *helperCalls) Call(ids []string, videoOn bool) error {
	ps := make([]protocol.Participant, len(ids))
	for i, id := range ids {
		ps[i] = protocol.Participant{TeamsUserID: id}
	}
	return h.send(protocol.VideoCommand{Type: protocol.VideoCall, Participants: ps, Video: videoOn})
}

func (h *helperCalls) Join(link string, videoOn bool) error {
	return h.send(protocol.VideoCommand{Type: protocol.VideoJoin, MeetingLink: link, Video: videoOn})
}

func (h *helperCalls) Accept(id string, videoOn bool) error {
	return h.send(protocol.VideoCommand{Type: protocol.VideoAccept, CallID: id, Video: videoOn})
}

func (h *helperCalls) Reject(id string) error {
	return h.send(protocol.VideoCommand{Type: protocol.VideoReject, CallID: id})
}

func (h *helperCalls) Hangup(id string) error {
	return h.send(protocol.VideoCommand{Type: protocol.VideoHangup, CallID: id})
}

func (h *helperCalls) Show() error { return h.send(protocol.VideoCommand{Type: protocol.VideoShow}) }
func (h *helperCalls) Hide() error { return h.send(protocol.VideoCommand{Type: protocol.VideoHide}) }

// Calls implements ui.CallSource: one helper per account, started lazily.
func (x *msalAccounts) Calls(accountID string) (ui.Calls, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if c, ok := x.calls[accountID]; ok {
		return c, nil
	}
	if x.relayURL == "" {
		return nil, errors.New("no relay configured (needed for the ACS token exchange)")
	}
	acc, ok := x.byID[accountID]
	if !ok {
		return nil, errors.New("unknown account")
	}
	cmd, args, err := helperCommand()
	if err != nil {
		return nil, err
	}
	ex, err := acs.NewExchanger(x.relayURL)
	if err != nil {
		return nil, err
	}
	tokens := acs.NewTokenSource(ex, x.acsEntraToken(acc))
	sup := video.New(video.Config{
		Command:     cmd,
		Args:        args,
		DisplayName: acc.Username(),
		Token: func(ctx context.Context) (string, error) {
			t, err := tokens.Token(ctx)
			return t.Value, err
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	hc := &helperCalls{sup: sup}
	go func() { _ = sup.Run(ctx) }()
	go hc.pump(ctx)
	x.calls[accountID] = hc
	x.stopCalls = append(x.stopCalls, cancel)
	return hc, nil
}

// acsEntraToken returns Entra tokens for the ACS scopes; the first time it
// asks the user to consent interactively (separate resource).
func (x *msalAccounts) acsEntraToken(acc auth.Account) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		tok, err := x.m.Token(ctx, acc, auth.ACSScopes)
		if errors.Is(err, auth.ErrInteractionRequired) {
			if cerr := x.m.Consent(ctx, acc, auth.ACSScopes); cerr != nil {
				return "", fmt.Errorf("calls need consent for Teams calling: %w", cerr)
			}
			tok, err = x.m.Token(ctx, acc, auth.ACSScopes)
		}
		return tok, err
	}
}

// StopCalls stops all video helpers.
func (x *msalAccounts) StopCalls() {
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, c := range x.stopCalls {
		c()
	}
	x.stopCalls = nil
}
