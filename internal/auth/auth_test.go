package auth

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
)

type blob struct{ data []byte }

func (b *blob) Marshal() ([]byte, error) { return b.data, nil }
func (b *blob) Unmarshal(d []byte) error { b.data = append([]byte(nil), d...); return nil }

func newCache(t *testing.T) *EncryptedFileCache {
	t.Helper()
	return &EncryptedFileCache{Path: filepath.Join(t.TempDir(), "sub", "msal.cache"), Keys: &MemoryKeyStore{}}
}

func TestCacheRoundTripIsEncrypted(t *testing.T) {
	c := newCache(t)
	ctx := context.Background()
	secret := []byte(`{"RefreshToken":{"x":{"secret":"refresh-token-value"}}}`)
	if err := c.Export(ctx, &blob{data: secret}, cache.ExportHints{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(c.Path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("refresh-token-value")) {
		t.Fatal("cache file contains plaintext")
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(c.Path)
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", st.Mode().Perm())
		}
	}
	var got blob
	if err := c.Replace(ctx, &got, cache.ReplaceHints{}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.data, secret) {
		t.Fatalf("round trip mismatch: %q", got.data)
	}
}

func TestCacheMissingFileOrKeyIsEmpty(t *testing.T) {
	c := newCache(t)
	ctx := context.Background()
	var got blob
	if err := c.Replace(ctx, &got, cache.ReplaceHints{}); err != nil || got.data != nil {
		t.Fatalf("missing file: %v %q", err, got.data)
	}
	_ = c.Export(ctx, &blob{data: []byte("x")}, cache.ExportHints{})
	_ = c.Keys.Delete() // keychain entry lost
	if err := c.Replace(ctx, &got, cache.ReplaceHints{}); err != nil || got.data != nil {
		t.Fatalf("missing key: %v %q", err, got.data)
	}
}

func TestCacheWrongKeyOrCorruptFileStartsFresh(t *testing.T) {
	c := newCache(t)
	ctx := context.Background()
	_ = c.Export(ctx, &blob{data: []byte("x")}, cache.ExportHints{})
	_ = c.Keys.Set(bytes.Repeat([]byte{7}, keySize))
	var got blob
	if err := c.Replace(ctx, &got, cache.ReplaceHints{}); err != nil || got.data != nil {
		t.Fatalf("wrong key: %v %q", err, got.data)
	}
	for _, junk := range [][]byte{nil, []byte("garbage"), append(append([]byte(nil), cacheMagic...), 1, 2, 3)} {
		_ = os.WriteFile(c.Path, junk, 0o600)
		if err := c.Replace(ctx, &got, cache.ReplaceHints{}); err != nil || got.data != nil {
			t.Fatalf("corrupt %q: %v", junk, err)
		}
	}
}

func TestCacheWipe(t *testing.T) {
	c := newCache(t)
	_ = c.Export(context.Background(), &blob{data: []byte("x")}, cache.ExportHints{})
	if err := c.Wipe(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("file still exists")
	}
	if _, err := c.Keys.Get(); !errors.Is(err, ErrNoKey) {
		t.Fatal("key still exists")
	}
	if err := c.Wipe(); err != nil {
		t.Fatalf("second wipe: %v", err)
	}
}

func TestCacheHonoursContext(t *testing.T) {
	c := newCache(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Export(ctx, &blob{}, cache.ExportHints{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("export: %v", err)
	}
	if err := c.Replace(ctx, &blob{}, cache.ReplaceHints{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("replace: %v", err)
	}
}

// --- Manager ---

type fakeMSAL struct {
	accounts    []public.Account
	silentCalls int
	silentErr   error
	expires     time.Time
	interactive public.Account
	removed     []string
}

func (f *fakeMSAL) Accounts(context.Context) ([]public.Account, error) { return f.accounts, nil }

func (f *fakeMSAL) AcquireTokenSilent(_ context.Context, _ []string, _ ...public.AcquireSilentOption) (public.AuthResult, error) {
	f.silentCalls++
	if f.silentErr != nil {
		return public.AuthResult{}, f.silentErr
	}
	return public.AuthResult{AccessToken: "silent-token", ExpiresOn: f.expires}, nil
}

func (f *fakeMSAL) AcquireTokenInteractive(_ context.Context, _ []string, _ ...public.AcquireInteractiveOption) (public.AuthResult, error) {
	f.accounts = append(f.accounts, f.interactive)
	return public.AuthResult{Account: f.interactive, AccessToken: "interactive-token", ExpiresOn: f.expires}, nil
}

func (f *fakeMSAL) RemoveAccount(_ context.Context, a public.Account) error {
	f.removed = append(f.removed, a.HomeAccountID)
	var rest []public.Account
	for _, x := range f.accounts {
		if x.HomeAccountID != a.HomeAccountID {
			rest = append(rest, x)
		}
	}
	f.accounts = rest
	return nil
}

type fakeWiper struct{ wiped int }

func (w *fakeWiper) Wipe() error { w.wiped++; return nil }

func TestManagerMultiAccountLoginTokenLogout(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	f := &fakeMSAL{expires: now.Add(time.Hour)}
	w := &fakeWiper{}
	m := newManager(f, w)
	m.now = func() time.Time { return now }
	ctx := context.Background()

	f.interactive = public.Account{HomeAccountID: "a.tenantA", PreferredUsername: "zoe@a.example", Realm: "tenantA"}
	accA, err := m.Login(ctx, "")
	if err != nil || accA.TenantID() != "tenantA" {
		t.Fatalf("login A: %+v %v", accA, err)
	}
	f.interactive = public.Account{HomeAccountID: "b.tenantB", PreferredUsername: "ann@b.example", Realm: "tenantB"}
	accB, _ := m.Login(ctx, "ann@b.example")

	accs, _ := m.Accounts(ctx)
	if len(accs) != 2 || accs[0].Username() != "ann@b.example" {
		t.Fatalf("accounts %+v", accs)
	}

	// Token from login is reused without a silent call.
	tok, err := m.Token(ctx, accA, GraphScopes)
	if err != nil || tok != "interactive-token" || f.silentCalls != 0 {
		t.Fatalf("token %q err %v silent %d", tok, err, f.silentCalls)
	}
	// Near expiry → silent refresh.
	now = now.Add(56 * time.Minute)
	f.expires = now.Add(time.Hour)
	tok, _ = m.Token(ctx, accA, GraphScopes)
	if tok != "silent-token" || f.silentCalls != 1 {
		t.Fatalf("expected silent refresh, got %q (%d calls)", tok, f.silentCalls)
	}
	// Different scope set → separate token.
	_, _ = m.GraphToken(accB)(ctx)
	_, _ = m.Token(ctx, accB, ACSScopes)
	if f.silentCalls != 3 { // B: graph token near expiry too + ACS
		t.Fatalf("silent calls %d", f.silentCalls)
	}

	if err := m.Logout(ctx, accA); err != nil || w.wiped != 0 {
		t.Fatalf("logout A: %v wiped=%d", err, w.wiped)
	}
	if err := m.Logout(ctx, accB); err != nil || w.wiped != 1 {
		t.Fatalf("logout B: %v wiped=%d", err, w.wiped)
	}
}

func TestManagerSilentFailureNeedsInteraction(t *testing.T) {
	f := &fakeMSAL{silentErr: errors.New("invalid_grant")}
	m := newManager(f, nil)
	_, err := m.Token(context.Background(), Account{msal: public.Account{HomeAccountID: "x"}}, GraphScopes)
	if !errors.Is(err, ErrInteractionRequired) {
		t.Fatalf("got %v", err)
	}
}

func TestNewRequiresClientID(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("want error")
	}
	m, err := New(Config{ClientID: "00000000-0000-0000-0000-000000000001", CachePath: filepath.Join(t.TempDir(), "c"), Keys: &MemoryKeyStore{}})
	if err != nil {
		t.Fatal(err)
	}
	accs, err := m.Accounts(context.Background())
	if err != nil || len(accs) != 0 {
		t.Fatalf("fresh manager: %v %v", accs, err)
	}
}
