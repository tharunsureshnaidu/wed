-- Migration 052 ran with the old versioned schema.
-- This migration 053 drops the deprecated columns (version, status, effective_from)
-- and removes the partial unique index so the table matches the new simple CRUD schema.

ALTER TABLE privacy_policies DROP COLUMN IF EXISTS version;
ALTER TABLE privacy_policies DROP COLUMN IF EXISTS status;
ALTER TABLE privacy_policies DROP COLUMN IF EXISTS effective_from;
DROP INDEX IF EXISTS idx_privacy_policies_single_active;

-- The seed row inserted by 052 (id=1) had version/status/effective_from which are
-- now gone. The title and content are still valid - keep the row.
-- No further inserts needed because content is managed via the Admin CRUD API.
