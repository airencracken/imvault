// SPDX-License-Identifier: AGPL-3.0-or-later

package ratelimit

import (
	"testing"
	"time"
)

// newTestLimiter returns a limiter with a controllable clock.
func newTestLimiter(perHour float64, burst int) (*Limiter, func(time.Duration)) {
	l := New(perHour, burst)

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }

	return l, func(d time.Duration) { now = now.Add(d) }
}

func TestDisabledLimiterAllowsEverything(t *testing.T) {
	for _, l := range []*Limiter{New(0, 0), New(10, 0), New(0, 10), nil} {
		if l.Enabled() {
			t.Errorf("limiter %+v should be disabled", l)
		}
		for i := 0; i < 1000; i++ {
			if ok, _ := l.Allow("anything"); !ok {
				t.Fatal("a disabled limiter rejected a request")
			}
		}
	}
}

func TestBurstIsAllowedThenThrottled(t *testing.T) {
	l, _ := newTestLimiter(3600, 5) // one per second, burst of five

	for i := 0; i < 5; i++ {
		if ok, _ := l.Allow("alice"); !ok {
			t.Fatalf("request %d was rejected inside the burst allowance", i+1)
		}
	}

	ok, retryAfter := l.Allow("alice")
	if ok {
		t.Fatal("the request past the burst allowance was allowed")
	}
	if retryAfter <= 0 || retryAfter > time.Second {
		t.Errorf("retryAfter = %s, want between 0 and 1s", retryAfter)
	}
}

func TestTokensRefillOverTime(t *testing.T) {
	l, advance := newTestLimiter(3600, 2) // one per second

	for i := 0; i < 2; i++ {
		l.Allow("alice")
	}
	if ok, _ := l.Allow("alice"); ok {
		t.Fatal("expected the bucket to be empty")
	}

	advance(1100 * time.Millisecond)
	if ok, _ := l.Allow("alice"); !ok {
		t.Error("a token did not refill after a second")
	}

	// The bucket never exceeds its capacity, even after a long quiet spell.
	advance(time.Hour)
	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow("alice"); !ok {
			t.Fatalf("burst request %d rejected after a long idle period", i+1)
		}
	}
	if ok, _ := l.Allow("alice"); ok {
		t.Error("the bucket refilled beyond its capacity")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l, _ := newTestLimiter(3600, 2)

	for i := 0; i < 2; i++ {
		l.Allow("alice")
	}
	if ok, _ := l.Allow("alice"); ok {
		t.Fatal("alice should be exhausted")
	}

	// Another key has its own budget.
	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow("bob"); !ok {
			t.Fatalf("bob request %d rejected because of alice's usage", i+1)
		}
	}
}

func TestRetryAfterMatchesTheRefillRate(t *testing.T) {
	// One token per minute means a ten-token deficit is roughly ten minutes.
	l, _ := newTestLimiter(60, 1)

	if ok, _ := l.Allow("alice"); !ok {
		t.Fatal("first request should pass")
	}

	// Drain ten further tokens' worth of deficit by repeated attempts.
	var retryAfter time.Duration
	for i := 0; i < 10; i++ {
		_, retryAfter = l.Allow("alice")
	}

	if retryAfter < 50*time.Second || retryAfter > 70*time.Second {
		t.Errorf("retryAfter = %s, want roughly a minute", retryAfter)
	}
}

func TestIdleBucketsAreSwept(t *testing.T) {
	l, advance := newTestLimiter(3600, 2)

	for i := 0; i < 50; i++ {
		l.Allow(string(rune('a' + i%26)))
	}
	if l.Buckets() == 0 {
		t.Fatal("expected buckets to be tracked")
	}

	// Idle long enough for the sweep to forget full buckets.
	advance(bucketTTL + gcInterval + time.Minute)
	l.Allow("trigger-gc")

	if got := l.Buckets(); got > 2 {
		t.Errorf("%d buckets survived the sweep, want the idle ones dropped", got)
	}
}

func TestConcurrentUseIsSafe(t *testing.T) {
	l := New(3600, 100)

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 200; j++ {
				l.Allow("shared")
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}

	if l.Buckets() != 1 {
		t.Errorf("tracked %d buckets, want 1", l.Buckets())
	}
}
