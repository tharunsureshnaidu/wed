-- Migration 070: Privacy Policy Versioning & Status Management

-- 1. Add version column if it doesn't exist
ALTER TABLE privacy_policies ADD COLUMN IF NOT EXISTS version VARCHAR(50);

-- Backfill version for any existing record(s)
UPDATE privacy_policies SET version = '1.0' WHERE version IS NULL;

ALTER TABLE privacy_policies ALTER COLUMN version SET NOT NULL;

-- Unique constraint on version
CREATE UNIQUE INDEX IF NOT EXISTS idx_privacy_policies_version ON privacy_policies(version);

-- 2. Add status column ('ACTIVE', 'INACTIVE')
ALTER TABLE privacy_policies ADD COLUMN IF NOT EXISTS status VARCHAR(20) NOT NULL DEFAULT 'INACTIVE';

-- Add check constraint for status
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chk_privacy_policies_status'
    ) THEN
        ALTER TABLE privacy_policies ADD CONSTRAINT chk_privacy_policies_status CHECK (status IN ('ACTIVE', 'INACTIVE'));
    END IF;
END $$;

-- 3. Add effective_from column
ALTER TABLE privacy_policies ADD COLUMN IF NOT EXISTS effective_from TIMESTAMPTZ;

-- If there are existing records and none are ACTIVE, activate the latest one
UPDATE privacy_policies
SET status = 'ACTIVE', effective_from = CURRENT_TIMESTAMP
WHERE id = (SELECT id FROM privacy_policies ORDER BY id DESC LIMIT 1)
  AND NOT EXISTS (SELECT 1 FROM privacy_policies WHERE status = 'ACTIVE');

-- 4. Ensure only one ACTIVE policy exists via partial unique index
CREATE UNIQUE INDEX IF NOT EXISTS idx_privacy_policies_single_active ON privacy_policies (status) WHERE status = 'ACTIVE';

-- 5. Ensure created_by and updated_by columns exist
ALTER TABLE privacy_policies ADD COLUMN IF NOT EXISTS created_by BIGINT REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE privacy_policies ADD COLUMN IF NOT EXISTS updated_by BIGINT REFERENCES users(id) ON DELETE SET NULL;
