// Package video supervises the acat-video helper (Electron + ACS Calling
// SDK): it starts the process, checks the protocol version, forwards
// commands as JSON lines, delivers events, keeps the ACS token fresh, and
// restarts the helper with backoff if it crashes.
package video

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/ape-dev-de/acat/pkg/protocol"
)

// TokenFunc returns a valid ACS access token.
type TokenFunc func(ctx context.Context) (string, error)

// Config configures the supervisor.
type Config struct {
	Command      string   // helper executable (electron binary or packaged app)
	Args         []string // e.g. the app directory for a dev electron
	Env          []string // extra environment
	Token        TokenFunc
	DisplayName  string
	ReadyTimeout time.Duration // default 30 s
	MinBackoff   time.Duration // default 1 s
	MaxBackoff   time.Duration // default 60 s
	// StableAfter resets the backoff if the helper ran at least this long.
	StableAfter time.Duration // default 2 min
}

// ErrVersionMismatch means the helper speaks another protocol version.
var ErrVersionMismatch = errors.New("video: helper protocol version mismatch")

// ErrNotRunning means no helper is currently connected.
var ErrNotRunning = errors.New("video: helper not running")

// Supervisor runs the helper.
type Supervisor struct {
	cfg    Config
	events chan protocol.VideoEvent

	mu    sync.Mutex
	stdin io.WriteCloser
	enc   *json.Encoder
}

// New creates a supervisor. Events (except ready/tokenExpired, which are
// handled internally) arrive on Events().
func New(cfg Config) *Supervisor {
	if cfg.ReadyTimeout <= 0 {
		cfg.ReadyTimeout = 30 * time.Second
	}
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = time.Minute
	}
	if cfg.StableAfter <= 0 {
		cfg.StableAfter = 2 * time.Minute
	}
	return &Supervisor{cfg: cfg, events: make(chan protocol.VideoEvent, 64)}
}

// Events delivers helper events.
func (s *Supervisor) Events() <-chan protocol.VideoEvent { return s.events }

// Send writes a command to the running helper.
func (s *Supervisor) Send(cmd protocol.VideoCommand) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enc == nil {
		return ErrNotRunning
	}
	return s.enc.Encode(cmd)
}

// Run starts the helper and restarts it until ctx ends or the helper has
// the wrong protocol version.
func (s *Supervisor) Run(ctx context.Context) error {
	backoff := s.cfg.MinBackoff
	for {
		started := time.Now()
		err := s.runOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ErrVersionMismatch) {
			s.emit(ctx, protocol.VideoEvent{Type: protocol.VideoError, Message: err.Error()})
			return err
		}
		s.emit(ctx, protocol.VideoEvent{Type: protocol.VideoError, Message: fmt.Sprintf("video helper stopped: %v", err)})
		if time.Since(started) >= s.cfg.StableAfter {
			backoff = s.cfg.MinBackoff
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, s.cfg.MaxBackoff)
	}
}

func (s *Supervisor) emit(ctx context.Context, e protocol.VideoEvent) {
	select {
	case s.events <- e:
	case <-ctx.Done():
	}
}

func (s *Supervisor) runOnce(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, s.cfg.Command, s.cfg.Args...) //nolint:gosec // our own helper binary
	cmd.Env = append(os.Environ(), s.cfg.Env...)
	cmd.Stderr = io.Discard // never surface helper logs (may contain tokens)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("video: start helper: %w", err)
	}
	exited := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(exited) }()
	defer func() {
		s.mu.Lock()
		s.enc, s.stdin = nil, nil
		s.mu.Unlock()
		_ = stdin.Close()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
	}()

	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			b := append([]byte(nil), sc.Bytes()...)
			select {
			case lines <- b:
			case <-ctx.Done():
				return
			}
		}
		readErr <- sc.Err()
	}()

	// Handshake: first line must be ready with our protocol version.
	select {
	case b := <-lines:
		var ev protocol.VideoEvent
		if json.Unmarshal(b, &ev) != nil || ev.Type != protocol.VideoReady {
			return errors.New("video: helper did not send ready")
		}
		if ev.Proto != protocol.VideoVersion {
			return fmt.Errorf("%w: helper %d, client %d", ErrVersionMismatch, ev.Proto, protocol.VideoVersion)
		}
	case <-exited:
		return fmt.Errorf("video: helper exited before ready: %w", waitErr)
	case <-time.After(s.cfg.ReadyTimeout):
		return errors.New("video: helper not ready in time")
	case <-ctx.Done():
		return ctx.Err()
	}

	s.mu.Lock()
	s.stdin, s.enc = stdin, json.NewEncoder(stdin)
	s.mu.Unlock()

	if err := s.sendToken(ctx, protocol.VideoInit); err != nil {
		return err
	}

	for {
		select {
		case b := <-lines:
			var ev protocol.VideoEvent
			if json.Unmarshal(b, &ev) != nil || ev.Type == "" {
				continue
			}
			if ev.Type == protocol.VideoTokenExpired {
				if err := s.sendToken(ctx, protocol.VideoRefreshToken); err != nil {
					s.emit(ctx, protocol.VideoEvent{Type: protocol.VideoError, Message: err.Error()})
				}
				continue
			}
			s.emit(ctx, ev)
		case err := <-readErr:
			if err == nil {
				err = io.EOF
			}
			return fmt.Errorf("video: helper output closed: %w", err)
		case <-exited:
			return fmt.Errorf("video: helper exited: %w", waitErr)
		case <-ctx.Done():
			_ = s.Send(protocol.VideoCommand{Type: protocol.VideoQuit})
			return ctx.Err()
		}
	}
}

func (s *Supervisor) sendToken(ctx context.Context, typ string) error {
	if s.cfg.Token == nil {
		return errors.New("video: no token source")
	}
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tok, err := s.cfg.Token(tctx)
	if err != nil {
		return fmt.Errorf("video: ACS token: %w", err)
	}
	return s.Send(protocol.VideoCommand{Type: typ, Proto: protocol.VideoVersion, Token: tok, DisplayName: s.cfg.DisplayName})
}
