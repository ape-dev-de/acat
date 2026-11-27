package relay

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// keyedLimiter is a token-bucket limiter per key (e.g. client IP). Idle
// entries are swept opportunistically, so memory stays bounded without a
// background goroutine.
type keyedLimiter struct {
	mu        sync.Mutex
	m         map[string]*limEntry
	r         rate.Limit
	b         int
	lastSweep time.Time
	now       func() time.Time
}

type limEntry struct {
	lim  *rate.Limiter
	seen time.Time
}

const limiterIdleTTL = 5 * time.Minute

func newKeyedLimiter(perSec float64, burst int) *keyedLimiter {
	return &keyedLimiter{m: map[string]*limEntry{}, r: rate.Limit(perSec), b: burst, now: time.Now}
}

func (k *keyedLimiter) allow(key string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	now := k.now()
	if now.Sub(k.lastSweep) > time.Minute {
		for kk, e := range k.m {
			if now.Sub(e.seen) > limiterIdleTTL {
				delete(k.m, kk)
			}
		}
		k.lastSweep = now
	}
	e := k.m[key]
	if e == nil {
		e = &limEntry{lim: rate.NewLimiter(k.r, k.b)}
		k.m[key] = e
	}
	e.seen = now
	return e.lim.AllowN(now, 1)
}

func (k *keyedLimiter) size() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.m)
}
