package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ape-dev-de/acat/internal/auth"
	"github.com/ape-dev-de/acat/internal/graph"
	"github.com/ape-dev-de/acat/internal/relayc"
	"github.com/ape-dev-de/acat/internal/subs"
	"github.com/ape-dev-de/acat/internal/ui"
	"github.com/ape-dev-de/acat/internal/version"
)

// msalAccounts adapts auth.Manager + graph.Client to ui.Accounts.
type msalAccounts struct {
	m        *auth.Manager
	relayURL string

	mu   sync.Mutex
	byID map[string]auth.Account
	apis map[string]*graph.Client
}

func newMSALAccounts(m *auth.Manager, relayURL string) *msalAccounts {
	return &msalAccounts{m: m, relayURL: relayURL, byID: map[string]auth.Account{}, apis: map[string]*graph.Client{}}
}

func info(a auth.Account) ui.AccountInfo {
	return ui.AccountInfo{ID: a.ID(), Username: a.Username(), TenantID: a.TenantID()}
}

func (x *msalAccounts) List(ctx context.Context) ([]ui.AccountInfo, error) {
	accs, err := x.m.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	out := make([]ui.AccountInfo, 0, len(accs))
	for _, a := range accs {
		x.byID[a.ID()] = a
		out = append(out, info(a))
	}
	return out, nil
}

func (x *msalAccounts) Login(ctx context.Context) (ui.AccountInfo, error) {
	a, err := x.m.Login(ctx, "")
	if err != nil {
		return ui.AccountInfo{}, err
	}
	x.mu.Lock()
	x.byID[a.ID()] = a
	x.mu.Unlock()
	return info(a), nil
}

func (x *msalAccounts) Logout(ctx context.Context, id string) error {
	x.mu.Lock()
	a, ok := x.byID[id]
	x.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown account")
	}
	if err := x.m.Logout(ctx, a); err != nil {
		return err
	}
	x.mu.Lock()
	delete(x.byID, id)
	delete(x.apis, id)
	x.mu.Unlock()
	return nil
}

func (x *msalAccounts) API(id string) ui.ChatAPI {
	x.mu.Lock()
	defer x.mu.Unlock()
	if c, ok := x.apis[id]; ok {
		return c
	}
	a := x.byID[id]
	c := graph.New(graph.TokenFunc(x.m.GraphToken(a)))
	x.apis[id] = c
	return c
}

// StartLive implements ui.LiveSource: relay connection + Graph subscription.
func (x *msalAccounts) StartLive(ctx context.Context, accountID, userID string, h ui.LiveHandler) (func(), error) {
	if x.relayURL == "" {
		return nil, errors.New("no relay configured (set relay_url in the config file or ACAT_RELAY_URL)")
	}
	api, ok := x.API(accountID).(*graph.Client)
	if !ok {
		return nil, errors.New("unknown account")
	}
	ctx, cancel := context.WithCancel(ctx)
	events := make(chan relayc.Event, 64)
	rc := relayc.New(x.relayURL, "acat/"+version.Version)
	mgr := subs.New(subs.Config{RelayURL: x.relayURL, UserID: userID}, api, h)
	done := make(chan struct{})
	go func() { _ = rc.Run(ctx, events) }()
	go func() { mgr.Run(ctx, events); close(done) }()
	return func() {
		cancel()
		select { // the manager deletes the subscription (max 10 s)
		case <-done:
		case <-time.After(12 * time.Second):
		}
	}, nil
}
