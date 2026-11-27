// Package config loads client settings: compiled-in defaults, overridden by
// <UserConfigDir>/acat/config.json, overridden by environment variables.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Build-time defaults for distributed binaries, e.g.
// -ldflags "-X github.com/ape-dev-de/acat/internal/config.DefaultClientID=…"
var (
	DefaultClientID = ""
	DefaultRelayURL = ""
)

// Config holds client settings.
type Config struct {
	ClientID string `json:"client_id,omitempty"` // Entra app (public client) ID
	RelayURL string `json:"relay_url,omitempty"` // e.g. wss://relay.example.org
}

// Dir returns the acat configuration directory.
func Dir() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	return filepath.Join(d, "acat"), nil
}

// CachePath is the encrypted MSAL token cache file.
func CachePath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "msal.cache"), nil
}

// Load returns the effective configuration.
func Load() (Config, error) {
	d, err := Dir()
	if err != nil {
		return Config{}, err
	}
	return load(filepath.Join(d, "config.json"), os.Getenv)
}

func load(path string, getenv func(string) string) (Config, error) {
	c := Config{ClientID: DefaultClientID, RelayURL: DefaultRelayURL}
	data, err := os.ReadFile(path) //nolint:gosec // path is our own config dir, not user input
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return c, fmt.Errorf("config: %w", err)
	default:
		var f Config
		if err := json.Unmarshal(data, &f); err != nil {
			return c, fmt.Errorf("config: %s: %w", path, err)
		}
		if f.ClientID != "" {
			c.ClientID = f.ClientID
		}
		if f.RelayURL != "" {
			c.RelayURL = f.RelayURL
		}
	}
	if v := getenv("ACAT_CLIENT_ID"); v != "" {
		c.ClientID = v
	}
	if v := getenv("ACAT_RELAY_URL"); v != "" {
		c.RelayURL = v
	}
	return c, nil
}
