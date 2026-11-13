-- Analytics no longer record who signed in or which file was opened. The
-- shared activity table belongs to Comfylib and may not exist yet on a new
-- database, so db.go deletes its stored rows after this file runs.
SELECT 1;
