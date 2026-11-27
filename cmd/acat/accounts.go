package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/ape-dev-de/acat/internal/auth"
	"github.com/ape-dev-de/acat/internal/graph"
	"github.com/ape-dev-de/acat/internal/ui"
)

// msalAccounts adapts auth.Manager + graph.Client to ui.Accounts.
type msalAccounts struct {
	m *auth.Manager

	mu   sync.Mutex
	byID map[string]auth.Account
	apis map[string]*graph.Client
}

func newMSALAccounts(m *auth.Manager) *msalAccounts {
	return &msalAccounts{m: m, byID: map[string]auth.Account{}, apis: map[string]*graph.Client{}}
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
