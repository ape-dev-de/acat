package auth

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

// File format: magic | 12-byte nonce | AES-256-GCM ciphertext.
var cacheMagic = []byte("ACAT-MSAL-1\n")

const cacheAAD = "acat msal cache v1"

// EncryptedFileCache persists the MSAL token cache as an encrypted file.
// It implements cache.ExportReplace.
type EncryptedFileCache struct {
	Path string
	Keys KeyStore

	mu sync.Mutex
}

var _ cache.ExportReplace = (*EncryptedFileCache)(nil)

// Replace loads the cache from disk into MSAL. A missing file, a missing
// key or undecryptable data yields an empty cache (the user signs in
// again) instead of an error, so a lost keychain entry is recoverable.
func (c *EncryptedFileCache) Replace(ctx context.Context, u cache.Unmarshaler, _ cache.ReplaceHints) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := os.ReadFile(c.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("auth: read cache: %w", err)
	}
	key, err := c.Keys.Get()
	if errors.Is(err, ErrNoKey) {
		return nil
	}
	if err != nil {
		return err
	}
	plain, err := decrypt(key, data)
	if err != nil {
		return nil //nolint:nilerr // corrupt or foreign cache: start fresh
	}
	return u.Unmarshal(plain)
}

// Export writes MSAL's cache to disk, encrypted, atomically, mode 0600.
func (c *EncryptedFileCache) Export(ctx context.Context, m cache.Marshaler, _ cache.ExportHints) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	plain, err := m.Marshal()
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	key, err := getOrCreateKey(c.Keys)
	if err != nil {
		return err
	}
	data, err := encrypt(key, plain)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil {
		return fmt.Errorf("auth: cache dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.Path), ".msal-*.tmp")
	if err != nil {
		return fmt.Errorf("auth: write cache: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil && !isWindows() {
		_ = tmp.Close()
		return fmt.Errorf("auth: write cache: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("auth: write cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("auth: write cache: %w", err)
	}
	if err := os.Rename(tmp.Name(), c.Path); err != nil {
		return fmt.Errorf("auth: write cache: %w", err)
	}
	return nil
}

// Wipe deletes the cache file and the key.
func (c *EncryptedFileCache) Wipe() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	err := os.Remove(c.Path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("auth: wipe cache: %w", err)
	}
	return c.Keys.Delete()
}

func encrypt(key, plain []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	_, _ = rand.Read(nonce)
	out := append([]byte(nil), cacheMagic...)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, plain, []byte(cacheAAD)), nil
}

func decrypt(key, data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, cacheMagic) {
		return nil, errors.New("auth: not an acat cache file")
	}
	data = data[len(cacheMagic):]
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, errors.New("auth: cache file truncated")
	}
	return gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], []byte(cacheAAD))
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
