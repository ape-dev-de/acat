// Package auth signs users in with MSAL (public client, auth code + PKCE,
// loopback redirect), keeps several accounts across tenants, and hands out
// access tokens. Tokens are cached in an encrypted file whose key lives in
// the OS keychain.
package auth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
)

// Authority for multi-tenant work/school accounts.
const Authority = "https://login.microsoftonline.com/organizations"

// GraphScopes are requested at sign-in. offline_access, openid and
// profile are added by MSAL automatically.
var GraphScopes = []string{
	"https://graph.microsoft.com/User.Read",
	"https://graph.microsoft.com/User.ReadBasic.All",
	"https://graph.microsoft.com/Chat.ReadWrite",
	"https://graph.microsoft.com/ChatMessage.Send",
	"https://graph.microsoft.com/Presence.Read.All",
	"https://graph.microsoft.com/Presence.ReadWrite",
}

// ACSScopes are needed for the Teams-user → ACS token exchange (M8). They
// belong to a different resource and must be requested separately.
var ACSScopes = []string{
	"https://auth.msft.communication.azure.com/Teams.ManageCalls",
	"https://auth.msft.communication.azure.com/Teams.ManageChats",
}

// ErrInteractionRequired means the account must sign in again.
var ErrInteractionRequired = errors.New("auth: interactive sign-in required")

// Account is a signed-in user in one tenant.
type Account struct {
	msal public.Account
}

// ID is stable across restarts (MSAL home account ID).
func (a Account) ID() string { return a.msal.HomeAccountID }

// Username is the UPN / e-mail used to sign in.
func (a Account) Username() string { return a.msal.PreferredUsername }

// TenantID is the account's tenant.
func (a Account) TenantID() string { return a.msal.Realm }

// msalClient is the subset of public.Client we use (fakeable in tests).
type msalClient interface {
	Accounts(ctx context.Context) ([]public.Account, error)
	AcquireTokenSilent(ctx context.Context, scopes []string, opts ...public.AcquireSilentOption) (public.AuthResult, error)
	AcquireTokenInteractive(ctx context.Context, scopes []string, opts ...public.AcquireInteractiveOption) (public.AuthResult, error)
	RemoveAccount(ctx context.Context, account public.Account) error
}

// wiper deletes persisted state when the last account signs out.
type wiper interface{ Wipe() error }

// Manager owns the MSAL client and the accounts.
type Manager struct {
	client msalClient
	cache  wiper
	now    func() time.Time

	mu     sync.Mutex
	tokens map[string]cachedToken // key: account ID + scope set
}

type cachedToken struct {
	value   string
	expires time.Time
}

// Config configures a Manager.
type Config struct {
	ClientID  string
	CachePath string   // encrypted MSAL cache file
	Keys      KeyStore // where the cache key lives
}

// New creates a Manager backed by MSAL.
func New(cfg Config) (*Manager, error) {
	if cfg.ClientID == "" {
		return nil, errors.New("auth: client ID is not configured (ACAT_CLIENT_ID)")
	}
	fc := &EncryptedFileCache{Path: cfg.CachePath, Keys: cfg.Keys}
	c, err := public.New(cfg.ClientID, public.WithAuthority(Authority), public.WithCache(fc))
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	return newManager(c, fc), nil
}

func newManager(c msalClient, w wiper) *Manager {
	return &Manager{client: c, cache: w, now: time.Now, tokens: map[string]cachedToken{}}
}

// Accounts returns all signed-in accounts, sorted by username.
func (m *Manager) Accounts(ctx context.Context) ([]Account, error) {
	accs, err := m.client.Accounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: accounts: %w", err)
	}
	out := make([]Account, 0, len(accs))
	for _, a := range accs {
		out = append(out, Account{msal: a})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username() < out[j].Username() })
	return out, nil
}

// Login opens the system browser for an interactive sign-in. loginHint
// may be empty.
func (m *Manager) Login(ctx context.Context, loginHint string) (Account, error) {
	var opts []public.AcquireInteractiveOption
	if loginHint != "" {
		opts = append(opts, public.WithLoginHint(loginHint))
	}
	res, err := m.client.AcquireTokenInteractive(ctx, GraphScopes, opts...)
	if err != nil {
		return Account{}, fmt.Errorf("auth: sign-in: %w", err)
	}
	acc := Account{msal: res.Account}
	m.remember(acc, GraphScopes, res)
	return acc, nil
}

// Logout removes one account. When no accounts remain, the cache file and
// its keychain key are deleted as well.
func (m *Manager) Logout(ctx context.Context, acc Account) error {
	if err := m.client.RemoveAccount(ctx, acc.msal); err != nil {
		return fmt.Errorf("auth: sign-out: %w", err)
	}
	m.mu.Lock()
	for k := range m.tokens {
		if strings.HasPrefix(k, acc.ID()+"|") {
			delete(m.tokens, k)
		}
	}
	m.mu.Unlock()

	rest, err := m.client.Accounts(ctx)
	if err != nil {
		return fmt.Errorf("auth: accounts: %w", err)
	}
	if len(rest) == 0 && m.cache != nil {
		return m.cache.Wipe()
	}
	return nil
}

// Token returns an access token for acc and scopes, refreshing silently
// when needed. It returns ErrInteractionRequired if MSAL cannot refresh.
func (m *Manager) Token(ctx context.Context, acc Account, scopes []string) (string, error) {
	key := tokenKey(acc, scopes)
	m.mu.Lock()
	if t, ok := m.tokens[key]; ok && m.now().Before(t.expires.Add(-5*time.Minute)) {
		m.mu.Unlock()
		return t.value, nil
	}
	m.mu.Unlock()

	res, err := m.client.AcquireTokenSilent(ctx, scopes, public.WithSilentAccount(acc.msal))
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w: %w", ErrInteractionRequired, err)
	}
	m.remember(acc, scopes, res)
	return res.AccessToken, nil
}

// GraphToken returns a function suitable as graph.TokenFunc for acc.
func (m *Manager) GraphToken(acc Account) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) { return m.Token(ctx, acc, GraphScopes) }
}

func (m *Manager) remember(acc Account, scopes []string, res public.AuthResult) {
	if res.AccessToken == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[tokenKey(acc, scopes)] = cachedToken{value: res.AccessToken, expires: res.ExpiresOn}
}

func tokenKey(acc Account, scopes []string) string {
	s := append([]string(nil), scopes...)
	sort.Strings(s)
	k := acc.ID() + "|"
	for _, x := range s {
		k += x + " "
	}
	return k
}
