package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPrecedence(t *testing.T) {
	DefaultClientID, DefaultRelayURL = "built-in", "wss://built-in"
	t.Cleanup(func() { DefaultClientID, DefaultRelayURL = "", "" })
	path := filepath.Join(t.TempDir(), "config.json")
	noEnv := func(string) string { return "" }

	c, err := load(path, noEnv)
	if err != nil || c.ClientID != "built-in" || c.RelayURL != "wss://built-in" {
		t.Fatalf("defaults: %+v %v", c, err)
	}

	_ = os.WriteFile(path, []byte(`{"client_id":"from-file"}`), 0o600)
	c, _ = load(path, noEnv)
	if c.ClientID != "from-file" || c.RelayURL != "wss://built-in" {
		t.Fatalf("file: %+v", c)
	}

	c, _ = load(path, func(k string) string {
		return map[string]string{"ACAT_CLIENT_ID": "from-env", "ACAT_RELAY_URL": "wss://env"}[k]
	})
	if c.ClientID != "from-env" || c.RelayURL != "wss://env" {
		t.Fatalf("env: %+v", c)
	}

	_ = os.WriteFile(path, []byte(`{`), 0o600)
	if _, err := load(path, noEnv); err == nil {
		t.Fatal("broken file must error")
	}
}
