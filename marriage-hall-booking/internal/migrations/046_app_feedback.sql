-- Feedback about the app itself, kept apart from `reviews`.
--
-- A review rates a venue and feeds that facility's avg_rating; this rates our
-- service and must not. Folding the two together would mean either a nullable
-- facility_id on reviews - so every rating query needs a guard it would
-- eventually forget - or app complaints silently dragging down a hall's score.
CREATE TABLE IF NOT EXISTS app_feedback (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Nullable: a user who cannot sign in is often the one with feedback, and
    -- ON DELETE SET NULL keeps their report after the account goes, which is
    -- what ops needs to see a pattern. Not CASCADE - deleting a user must not
    -- erase the evidence of the bug they reported.
    user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    rating INT CHECK (rating BETWEEN 1 AND 5),
    message TEXT NOT NULL,
    -- Path under /uploads, same as facility images. One screenshot per report:
    -- the screen offers a single Add Photo button.
    attachment_url VARCHAR(500),
    -- What the reporter was running. A bug report without a build number costs
    -- a round trip to make actionable.
    app_version VARCHAR(50),
    platform VARCHAR(10) CHECK (platform IN ('ANDROID', 'IOS', 'WEB')),

    status VARCHAR(20) NOT NULL DEFAULT 'NEW'
        CHECK (status IN ('NEW', 'REVIEWING', 'RESOLVED', 'CLOSED')),
    -- Ops notes, never shown to the reporter.
    admin_note TEXT,
    resolved_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    resolved_at TIMESTAMPTZ,

    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

-- The ops inbox: open items, newest first.
CREATE INDEX IF NOT EXISTS idx_app_feedback_triage
    ON app_feedback (status, created_at DESC) WHERE is_deleted = FALSE;

-- One user's own submissions.
CREATE INDEX IF NOT EXISTS idx_app_feedback_user
    ON app_feedback (user_id, created_at DESC) WHERE is_deleted = FALSE;
