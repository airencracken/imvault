// SPDX-License-Identifier: AGPL-3.0-or-later

package ratelimit

import (
	"math/rand/v2"
	"strconv"
	"sync"
	"testing"
	"time"
)

// newCappedLimiter returns a test limiter that tracks at most max keys.
func newCappedLimiter(perHour float64, burst, max int) (*Limiter, func(time.Duration)) {
	l, advance := newTestLimiter(perHour, burst)
	l.maxKeys = max
	return l, advance
}

func TestTheDefaultLimiterIsCapped(t *testing.T) {
	l := New(3600, 5)
	if l.maxKeys != defaultMaxKeys || defaultMaxKeys <= 0 {
		t.Fatalf("maxKeys = %d, want %d", l.maxKeys, defaultMaxKeys)
	}
	for i := 0; i < defaultMaxKeys+500; i++ {
		l.Allow("203.0.113." + strconv.Itoa(i))
	}
	if got := l.Buckets(); got != defaultMaxKeys {
		t.Fatalf("tracked %d keys, want the cap of %d", got, defaultMaxKeys)
	}
}

// A flood of fresh keys, as from a client cycling through addresses, must not
// grow the table, and must not free a key that is still being refused.
func TestAFloodOfNewKeysCannotFreeAnActiveOne(t *testing.T) {
	l, _ := newCappedLimiter(3600, 2, 3)
	for i := 0; i < 2; i++ {
		l.Allow("alice")
	}
	for i := 0; i < 100; i++ {
		if ok, _ := l.Allow("alice"); ok {
			t.Fatalf("alice got a fresh budget after %d other keys", i)
		}
		l.Allow("flood-" + strconv.Itoa(i))
		if got := l.Buckets(); got > 3 {
			t.Fatalf("tracked %d keys, want at most 3", got)
		}
	}
}

func TestTheLeastRecentlyUsedKeyIsTheOneForgotten(t *testing.T) {
	l, _ := newCappedLimiter(3600, 1, 2)
	l.Allow("old")
	l.Allow("recent")
	l.Allow("recent")
	l.Allow("new") // full: forgets "old", the least recently used
	if ok, _ := l.Allow("recent"); ok {
		t.Fatal("the recently used key was forgotten instead of the oldest")
	}
	if ok, _ := l.Allow("old"); !ok {
		t.Fatal("the oldest key was kept; it should have been forgotten and start afresh")
	}
}

// Property: whatever sequence of keys arrives, the table never exceeds its
// cap, every tracked key appears exactly once in the recency order, and the
// key used last is tracked.
func TestCapInvariantsHoldForRandomTraffic(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 2))
	for round := 0; round < 200; round++ {
		max := 1 + random.IntN(8)
		l, advance := newCappedLimiter(3600, 1+random.IntN(3), max)
		for step := 0; step < 200; step++ {
			key := strconv.Itoa(random.IntN(3 * max))
			l.Allow(key)
			advance(time.Duration(random.IntN(5000)) * time.Millisecond)
			if len(l.buckets) > max || len(l.buckets) != l.order.Len() {
				t.Fatalf("round %d: %d buckets, %d in order, cap %d", round, len(l.buckets), l.order.Len(), max)
			}
			if front := l.order.Front().Value.(*bucket); front.key != key || l.buckets[key] == nil {
				t.Fatalf("round %d: the key just used is not the most recent", round)
			}
		}
	}
}

func TestSweepAndCapAgreeUnderConcurrency(t *testing.T) {
	l, _ := newCappedLimiter(3600, 2, 50)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				l.Allow(strconv.Itoa(worker) + "-" + strconv.Itoa(i))
			}
		}()
	}
	wg.Wait()
	if got := l.Buckets(); got != 50 || l.order.Len() != 50 {
		t.Fatalf("tracked %d keys and %d in order, want 50", got, l.order.Len())
	}
}
