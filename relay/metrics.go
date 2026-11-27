package relay

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync/atomic"
)

// Metrics are aggregate counters only. Labels come from fixed sets, never
// from request data, so nothing identifying can end up here.
type metrics struct {
	wsActive    atomic.Int64
	wsAccepted  atomic.Uint64
	wsRejected  counterVec // reason
	validations counterVec // kind
	forwarded   counterVec // kind
	dropped     counterVec // reason
}

// Drop and reject reasons (fixed label values).
const (
	reasonUnknownToken = "unknown_token"
	reasonInvalid      = "invalid"
	reasonTooLarge     = "too_large"
	reasonRateLimit    = "ratelimit"
	reasonSlowConsumer = "slow_consumer"
	reasonTooMany      = "too_many_conns"
	reasonHandshake    = "handshake"
	reasonVersion      = "version"
)

type counterVec map[string]*atomic.Uint64

func newCounterVec(labels ...string) counterVec {
	v := counterVec{}
	for _, l := range labels {
		v[l] = new(atomic.Uint64)
	}
	return v
}

func (v counterVec) inc(label string) {
	if c, ok := v[label]; ok {
		c.Add(1)
	}
}

func (v counterVec) get(label string) uint64 {
	if c, ok := v[label]; ok {
		return c.Load()
	}
	return 0
}

func newMetrics() *metrics {
	return &metrics{
		wsRejected:  newCounterVec(reasonRateLimit, reasonTooMany, reasonHandshake, reasonVersion),
		validations: newCounterVec(kindNotification, kindLifecycle),
		forwarded:   newCounterVec(kindNotification, kindLifecycle),
		dropped:     newCounterVec(reasonUnknownToken, reasonInvalid, reasonTooLarge, reasonRateLimit, reasonSlowConsumer),
	}
}

func (m *metrics) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		m.write(w)
	})
}

func (m *metrics) write(w io.Writer) {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	vec := func(name, help, label string, v counterVec) {
		p("# HELP %s %s\n# TYPE %s counter\n", name, help, name)
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p("%s{%s=%q} %d\n", name, label, k, v[k].Load())
		}
	}
	p("# HELP acat_relay_ws_connections Open WebSocket connections.\n# TYPE acat_relay_ws_connections gauge\n")
	p("acat_relay_ws_connections %d\n", m.wsActive.Load())
	p("# HELP acat_relay_ws_accepted_total Accepted WebSocket connections.\n# TYPE acat_relay_ws_accepted_total counter\n")
	p("acat_relay_ws_accepted_total %d\n", m.wsAccepted.Load())
	vec("acat_relay_ws_rejected_total", "Rejected WebSocket connections.", "reason", m.wsRejected)
	vec("acat_relay_hook_validations_total", "Answered Graph validation requests.", "kind", m.validations)
	vec("acat_relay_hook_forwarded_total", "Notifications forwarded to a client.", "kind", m.forwarded)
	vec("acat_relay_hook_dropped_total", "Notifications dropped.", "reason", m.dropped)
}
