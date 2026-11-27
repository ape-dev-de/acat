//go:build load

package relay

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/ape-dev-de/acat/pkg/protocol"
)

// TestLoad holds many WebSocket clients open and pushes notifications to a
// sample of them. Needs a high fd limit (ulimit -n 65536):
//
//	ACAT_LOAD_CONNS=10000 go test -tags load -run TestLoad -v ./relay/
func TestLoad(t *testing.T) {
	n := 10000
	if v, err := strconv.Atoi(os.Getenv("ACAT_LOAD_CONNS")); err == nil && v > 0 {
		n = v
	}
	r := newTestRelay(t, func(c *Config) {
		c.MaxConnsPerIP = n + 10
		c.WSConnectRate = 1e9
		c.WSConnectBurst = n
		c.HookRate = 1e9
		c.HookBurst = n
		c.PingInterval = 30 * time.Second
		c.HelloTimeout = 30 * time.Second
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	type client struct {
		conn  *websocket.Conn
		token string
	}
	clients := make([]client, n)
	var wg sync.WaitGroup
	errs := make(chan error, n)
	sem := make(chan struct{}, 64)
	start := time.Now()
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			// Retry: a small OS listen backlog (macOS somaxconn=128) can reset
			// connections under a burst of dials; that is not a relay issue.
			var conn *websocket.Conn
			var err error
			for attempt := range 5 {
				if conn, _, err = websocket.Dial(ctx, r.wsURL(), nil); err == nil {
					break
				}
				time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
			}
			if err != nil {
				errs <- fmt.Errorf("dial %d: %w", i, err)
				return
			}
			if err := wsjson.Write(ctx, conn, protocol.Hello{Type: protocol.TypeHello, Proto: protocol.RelayVersion, Client: "load"}); err != nil {
				errs <- err
				return
			}
			var w protocol.Welcome
			if err := wsjson.Read(ctx, conn, &w); err != nil {
				errs <- fmt.Errorf("welcome %d: %w", i, err)
				return
			}
			clients[i] = client{conn: conn, token: w.RoutingToken}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	t.Logf("%d connections in %v", n, time.Since(start).Round(time.Millisecond))
	defer func() {
		for _, c := range clients {
			_ = c.conn.CloseNow()
		}
	}()

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	// Includes the in-process client side, so this is an upper bound.
	perConn := (int64(after.HeapInuse) - int64(before.HeapInuse)) / int64(n)
	t.Logf("heap in use: %d MB total, ~%d KB per connection (client + server)", after.HeapInuse>>20, perConn>>10)

	// Deliver to a random sample and measure end-to-end latency.
	sample := min(1000, n)
	idx := rand.Perm(n)[:sample]
	lat := make([]time.Duration, sample)
	// Graph reuses connections; so does this client.
	hc := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 64, MaxConnsPerHost: 64}}
	var dwg sync.WaitGroup
	for k, i := range idx {
		dwg.Add(1)
		go func() {
			defer dwg.Done()
			t0 := time.Now()
			resp, err := hc.Post(r.http.URL+"/hook?r="+url.QueryEscape(clients[i].token), "application/json", strings.NewReader(sampleNotification))
			if err != nil {
				t.Errorf("post: %v", err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusAccepted {
				t.Errorf("status %d", resp.StatusCode)
				return
			}
			var p protocol.Push
			if err := wsjson.Read(ctx, clients[i].conn, &p); err != nil {
				t.Errorf("read %d: %v", i, err)
				return
			}
			lat[k] = time.Since(t0)
		}()
	}
	dwg.Wait()
	if t.Failed() {
		t.FailNow()
	}
	slices.Sort(lat)
	p50, p99 := lat[sample/2], lat[sample*99/100]
	t.Logf("delivery latency over %d pushes: p50 %v, p99 %v", sample, p50, p99)
	if p99 > 2*time.Second {
		t.Fatalf("p99 latency %v exceeds 2s", p99)
	}
	if got := r.srv.hub.len(); got != n {
		t.Fatalf("hub has %d clients, want %d", got, n)
	}
}
