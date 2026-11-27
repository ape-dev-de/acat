package single

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Unix socket paths are limited to ~104 bytes; t.TempDir() can be too long
// on macOS, so use a short directory.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "acat")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func TestSecondInstanceAsksFirstToShow(t *testing.T) {
	dir := shortDir(t)
	shown := make(chan struct{}, 1)
	first, err := Acquire(dir, func() { shown <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()

	if _, err := Acquire(dir, nil); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second: %v", err)
	}
	select {
	case <-shown:
	case <-time.After(3 * time.Second):
		t.Fatal("first instance was not asked to show")
	}
}

func TestStaleSocketIsReplaced(t *testing.T) {
	dir := shortDir(t)
	if err := os.WriteFile(filepath.Join(dir, "acat.sock"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	in, err := Acquire(dir, nil)
	if err != nil {
		t.Fatalf("stale socket not replaced: %v", err)
	}
	in.Release()
	if _, err := os.Stat(filepath.Join(dir, "acat.sock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("socket not removed on release")
	}
	// After release, a new primary can start.
	again, err := Acquire(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	again.Release()
}
