-- Migration 051: Make app feedback message optional for rating-only submissions,
-- and add indexes for filtering by rating and sorting by created_at.

ALTER TABLE app_feedback ALTER COLUMN message DROP NOT NULL;
ALTER TABLE app_feedback ALTER COLUMN message SET DEFAULT '';

-- Fast lookup for admin filtering by rating and summary stats
CREATE INDEX IF NOT EXISTS idx_app_feedback_rating
    ON app_feedback (rating) WHERE is_deleted = FALSE;

-- Fast ordering for admin list by newest/oldest
CREATE INDEX IF NOT EXISTS idx_app_feedback_created_at
    ON app_feedback (created_at DESC) WHERE is_deleted = FALSE;
