//go:build electron

package video

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ape-dev-de/acat/pkg/protocol"
)

// Runs the real helper from video/ (npm ci && npm run build first):
//
//	go test -tags electron ./internal/video/
func TestRealHelper(t *testing.T) {
	root, _ := filepath.Abs("../../video")
	bin := filepath.Join(root, "node_modules", "electron", "dist", "electron")
	switch runtime.GOOS {
	case "darwin":
		bin = filepath.Join(root, "node_modules", "electron", "dist", "Electron.app", "Contents", "MacOS", "Electron")
	case "windows":
		bin += ".exe"
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skip("electron not installed:", err)
	}
	s := New(Config{
		Command: bin,
		Args:    []string{root},
		Token:   func(context.Context) (string, error) { return "invalid.token.value", nil },
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	// The handshake succeeded once init reaches the renderer; an invalid
	// token must produce an error event, not a crash.
	timeout := time.After(45 * time.Second)
	for {
		select {
		case ev := <-s.Events():
			t.Logf("event: %s %s %s", ev.Type, ev.Command, ev.Message)
			if ev.Type == protocol.VideoError && ev.Command == protocol.VideoInit {
				if strings.Contains(ev.Message, "invalid.token.value") {
					t.Fatal("token leaked into error message")
				}
				cancel()
				<-done
				return
			}
			if ev.Type == protocol.VideoError && strings.Contains(ev.Message, "stopped") {
				t.Fatalf("helper stopped: %s", ev.Message)
			}
		case <-timeout:
			t.Fatal("no init error event")
		}
	}
}
