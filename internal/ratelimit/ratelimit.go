// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ratelimit provides a small in-memory token bucket, keyed by an
// arbitrary string such as an account or a client address.
//
// It is deliberately process-local: a single imvault instance is the target, and
// the budget is there to stop a runaway script or a careless client, not to
// enforce a global contract across a fleet. Restarting the server resets the
// budgets, which is an acceptable trade for having no extra dependency.
package ratelimit

import (
	"math"
	"sync"
	"time"
)

const (
	// gcInterval is how often idle buckets are swept.
	gcInterval = 5 * time.Minute
	// bucketTTL is how long a full, unused bucket is kept before being
	// forgotten. A returning client simply starts with a full budget.
	bucketTTL = 15 * time.Minute
)

// Limiter is a set of token buckets, one per key.
//
// A zero-value or disabled Limiter allows everything, so callers can wire one in
// unconditionally.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket

	// rate is tokens added per second; burst is the bucket capacity.
	rate  float64
	burst float64

	enabled bool
	now     func() time.Time
	lastGC  time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New returns a Limiter allowing perHour events per hour with the given burst
// capacity. A perHour of zero or less disables limiting entirely.
func New(perHour float64, burst int) *Limiter {
	if perHour <= 0 || burst <= 0 {
		return &Limiter{}
	}
	return &Limiter{
		buckets: make(map[string]*bucket),
		rate:    perHour / 3600,
		burst:   float64(burst),
		enabled: true,
		now:     time.Now,
	}
}

// Enabled reports whether the limiter will ever reject anything.
func (l *Limiter) Enabled() bool { return l != nil && l.enabled }

// Allow consumes one token for key. When it returns false, retryAfter is how
// long the caller should wait before trying again.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	if !l.Enabled() {
		return true, 0
	}

	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.gcLocked(now)

	b, exists := l.buckets[key]
	if !exists {
		// A new key starts with a full budget, so a first request is never
		// rejected.
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	} else if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
		b.last = now
	}

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}

	missing := 1 - b.tokens
	return false, time.Duration(missing / l.rate * float64(time.Second))
}

// Buckets reports how many keys are currently tracked, which is useful in tests
// and for a metrics endpoint.
func (l *Limiter) Buckets() int {
	if !l.Enabled() {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// gcLocked drops buckets that have refilled and have been idle for a while.
// Callers must hold the lock.
func (l *Limiter) gcLocked(now time.Time) {
	if now.Sub(l.lastGC) < gcInterval {
		return
	}
	l.lastGC = now

	for key, b := range l.buckets {
		// A bucket that has been idle long enough to refill is
		// indistinguishable from a brand new one, so it can be forgotten.
		// Project the refill, since only the key being requested gets updated
		// on the request path.
		projected := math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
		if projected >= l.burst && now.Sub(b.last) > bucketTTL {
			delete(l.buckets, key)
		}
	}
}
