package pathsafe

import (
	"strings"
	"sync"
	"time"
)

// RateLimiter bounds writes per author identity with a token bucket per
// author. Authors are the strings the CRDT layer uses: "user:<id>",
// "agent:<label>", "filesystem". Agents get a lower rate than users so a
// runaway loop cannot fill the disk.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	user    Rate
	agent   Rate
	now     func() time.Time
}

// Rate is N events per Window.
type Rate struct {
	N      int
	Window time.Duration
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewRateLimiter builds a limiter with the given per-window rates.
func NewRateLimiter(user, agent Rate) *RateLimiter {
	return &RateLimiter{
		buckets: map[string]*bucket{},
		user:    user,
		agent:   agent,
		now:     time.Now,
	}
}

// DefaultRateLimiter is 100 writes/min for users and 30 writes/min for
// agents.
func DefaultRateLimiter() *RateLimiter {
	return NewRateLimiter(Rate{100, time.Minute}, Rate{30, time.Minute})
}

func (l *RateLimiter) rateFor(author string) Rate {
	if strings.HasPrefix(author, "agent:") {
		return l.agent
	}
	return l.user
}

// Allow consumes one token for author and reports whether the write may
// proceed. The "filesystem" author is never limited: those writes already
// happened on disk and refusing to observe them would only desynchronise
// the index.
func (l *RateLimiter) Allow(author string) bool {
	if author == "filesystem" {
		return true
	}
	rate := l.rateFor(author)
	if rate.N <= 0 || rate.Window <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[author]
	if !ok {
		b = &bucket{tokens: float64(rate.N), last: now}
		l.buckets[author] = b
	}
	refill := now.Sub(b.last).Seconds() * float64(rate.N) / rate.Window.Seconds()
	b.tokens += refill
	if b.tokens > float64(rate.N) {
		b.tokens = float64(rate.N)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
