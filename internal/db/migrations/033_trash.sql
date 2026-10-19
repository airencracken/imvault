-- Trashed files keep their content references and quota until purged.
ALTER TABLE files ADD COLUMN trashed_at INTEGER CHECK (trashed_at IS NULL OR (typeof(trashed_at) = 'integer' AND trashed_at > 0 AND user_id IS NOT NULL));
CREATE INDEX files_trash ON files(user_id, trashed_at DESC, id DESC) WHERE trashed_at IS NOT NULL;
