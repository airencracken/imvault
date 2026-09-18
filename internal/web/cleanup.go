// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"time"
)

// cleanupBatch is how many expired rows are reaped per query.
const cleanupBatch = 200

// maxCleanupRounds bounds a single cleanup pass so a huge backlog cannot pin
// the process in one loop.
const maxCleanupRounds = 50

// cleanupLoop reaps expired uploads and stale sessions until ctx is cancelled.
func (s *Server) cleanupLoop(ctx context.Context) {
	interval := s.cfg.CleanupInterval
	if interval <= 0 {
		interval = 15 * time.Minute
	}

	s.runCleanup(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runCleanup(ctx)
		}
	}
}

// runCleanup deletes expired files, then prunes orphaned tags and sessions.
func (s *Server) runCleanup(ctx context.Context) {
	now := time.Now()
	files := 0

	for round := 0; round < maxCleanupRounds; round++ {
		batch, err := s.store.ExpiredFiles(ctx, now, cleanupBatch)
		if err != nil {
			s.log.Error("cleanup: list expired files", "error", err)
			return
		}
		if len(batch) == 0 {
			break
		}

		for _, f := range batch {
			// Row first, then bytes: a crash mid-way leaves recoverable
			// orphans rather than dangling references.
			if err := s.store.DeleteFile(ctx, f.ID); err != nil {
				s.log.Error("cleanup: delete file row", "id", f.ID, "error", err)
				continue
			}
			s.deleteFileObjects(f)
			files++
		}

		if len(batch) < cleanupBatch {
			break
		}
		if ctx.Err() != nil {
			return
		}
	}

	tags, err := s.store.PruneUnusedTags(ctx)
	if err != nil {
		s.log.Error("cleanup: prune tags", "error", err)
	}

	sessions, err := s.store.DeleteExpiredSessions(ctx, now)
	if err != nil {
		s.log.Error("cleanup: prune sessions", "error", err)
	}

	authTokens, err := s.store.DeleteExpiredAuthTokens(ctx, now)
	if err != nil {
		s.log.Error("cleanup: prune auth tokens", "error", err)
	}

	apiKeys, err := s.store.DeleteExpiredAPIKeys(ctx, now)
	if err != nil {
		s.log.Error("cleanup: prune api keys", "error", err)
	}

	// Delivered messages are kept briefly for reference, then dropped.
	sentMail, err := s.store.PruneSentMail(ctx, now.Add(-sentMailRetention))
	if err != nil {
		s.log.Error("cleanup: prune sent mail", "error", err)
	}

	if files > 0 || tags > 0 || sessions > 0 || authTokens > 0 || apiKeys > 0 || sentMail > 0 {
		s.log.Info("cleanup complete",
			"files_deleted", files,
			"tags_pruned", tags,
			"sessions_pruned", sessions,
			"auth_tokens_pruned", authTokens,
			"api_keys_pruned", apiKeys,
			"sent_mail_pruned", sentMail,
		)
	}
}
