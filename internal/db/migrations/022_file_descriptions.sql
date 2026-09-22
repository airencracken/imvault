-- Descriptions belong to each upload, even when its bytes are shared.
ALTER TABLE files ADD COLUMN description TEXT NOT NULL DEFAULT ''
    CHECK (length(description) <= 1000 AND instr(description, char(0)) = 0);
