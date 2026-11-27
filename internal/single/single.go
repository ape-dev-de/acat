// Package single makes sure only one acat instance runs per user. The
// first instance listens on a Unix domain socket in the config directory
// (also supported on Windows 10+); later instances ask it to show its
// window and exit.
package single

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const msgShow = "show"

// Instance is the running primary instance.
type Instance struct {
	ln   net.Listener
	path string
}

// ErrAlreadyRunning is returned by Acquire when another instance answered.
var ErrAlreadyRunning = errors.New("acat is already running")

// Acquire becomes the primary instance or, if one is running, asks it to
// show itself and returns ErrAlreadyRunning. onShow is called (from a
// background goroutine) whenever a later instance starts.
func Acquire(dir string, onShow func()) (*Instance, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "acat.sock")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var d net.Dialer
	if conn, err := d.DialContext(ctx, "unix", path); err == nil {
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = conn.Write([]byte(msgShow + "\n"))
		_ = conn.Close()
		return nil, ErrAlreadyRunning
	}
	_ = os.Remove(path) // stale socket from a crash

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	in := &Instance{ln: ln, path: path}
	go in.serve(onShow)
	return in, nil
}

func (in *Instance) serve(onShow func()) {
	for {
		conn, err := in.ln.Accept()
		if err != nil {
			return
		}
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		line, _ := bufio.NewReader(conn).ReadString('\n')
		_ = conn.Close()
		if strings.TrimSpace(line) == msgShow && onShow != nil {
			onShow()
		}
	}
}

// Release stops listening and removes the socket.
func (in *Instance) Release() {
	if in == nil {
		return
	}
	_ = in.ln.Close()
	_ = os.Remove(in.path)
}
