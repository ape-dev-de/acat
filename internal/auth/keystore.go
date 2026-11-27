package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"

	"github.com/zalando/go-keyring"
)

// KeyStore holds the cache encryption key. Production uses the OS keychain
// (macOS Keychain, Windows Credential Manager, Secret Service on Linux).
// Only the 32-byte key lives there, because Windows limits a credential
// to ~2.5 KB and a multi-account MSAL cache is larger.
type KeyStore interface {
	Get() ([]byte, error) // ErrNoKey if absent
	Set(key []byte) error
	Delete() error
}

// ErrNoKey means no key has been stored yet.
var ErrNoKey = errors.New("auth: no key stored")

const keySize = 32

// KeyringStore stores the key in the OS keychain.
type KeyringStore struct {
	Service string // e.g. "acat"
	User    string // e.g. "msal-cache-key"
}

// Get implements KeyStore.
func (k KeyringStore) Get() ([]byte, error) {
	s, err := keyring.Get(k.Service, k.User)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, ErrNoKey
	}
	if err != nil {
		return nil, fmt.Errorf("auth: keychain: %w", err)
	}
	key, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(key) != keySize {
		return nil, fmt.Errorf("auth: keychain entry is corrupt")
	}
	return key, nil
}

// Set implements KeyStore.
func (k KeyringStore) Set(key []byte) error {
	if err := keyring.Set(k.Service, k.User, base64.StdEncoding.EncodeToString(key)); err != nil {
		return fmt.Errorf("auth: keychain: %w", err)
	}
	return nil
}

// Delete implements KeyStore.
func (k KeyringStore) Delete() error {
	err := keyring.Delete(k.Service, k.User)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("auth: keychain: %w", err)
	}
	return nil
}

// MemoryKeyStore is an in-memory KeyStore for tests and demo mode.
type MemoryKeyStore struct {
	mu  sync.Mutex
	key []byte
}

// Get implements KeyStore.
func (m *MemoryKeyStore) Get() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.key == nil {
		return nil, ErrNoKey
	}
	return append([]byte(nil), m.key...), nil
}

// Set implements KeyStore.
func (m *MemoryKeyStore) Set(key []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.key = append([]byte(nil), key...)
	return nil
}

// Delete implements KeyStore.
func (m *MemoryKeyStore) Delete() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.key = nil
	return nil
}

// getOrCreateKey returns the stored key, creating one if none exists.
func getOrCreateKey(ks KeyStore) ([]byte, error) {
	key, err := ks.Get()
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, ErrNoKey) {
		return nil, err
	}
	key = make([]byte, keySize)
	_, _ = rand.Read(key)
	if err := ks.Set(key); err != nil {
		return nil, err
	}
	return key, nil
}
