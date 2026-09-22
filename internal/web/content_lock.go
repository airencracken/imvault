// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"sync"
)

// Content uploads and deletion share a lock per original. A slow S3 delete
// must not race a new reference to the same bytes. Unrelated uploads proceed.
type contentLocks struct {
	mu      sync.Mutex
	entries map[string]*contentLock
}

type contentLock struct {
	ready chan struct{}
	users int
}

func (l *contentLocks) acquire(ctx context.Context, sha string) (func(), error) {
	l.mu.Lock()
	if l.entries == nil {
		l.entries = map[string]*contentLock{}
	}
	entry := l.entries[sha]
	if entry == nil {
		entry = &contentLock{ready: make(chan struct{}, 1)}
		entry.ready <- struct{}{}
		l.entries[sha] = entry
	}
	entry.users++
	l.mu.Unlock()
	select {
	case <-ctx.Done():
		l.releaseRef(sha, entry)
		return nil, ctx.Err()
	case <-entry.ready:
		return func() { entry.ready <- struct{}{}; l.releaseRef(sha, entry) }, nil
	}
}

func (l *contentLocks) releaseRef(sha string, entry *contentLock) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry.users--
	if entry.users == 0 {
		delete(l.entries, sha)
	}
}
