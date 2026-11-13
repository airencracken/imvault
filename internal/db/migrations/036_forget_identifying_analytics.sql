-- Analytics no longer record who signed in or which file was opened. The
-- per-account table belongs to Comfylib, so db.go drops it with
-- analytics.RemoveDetail after this file runs.
SELECT 1;
