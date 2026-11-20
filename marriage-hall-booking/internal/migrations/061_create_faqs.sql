-- Global / Platform Frequently Asked Questions (FAQ) management.
-- Supports rich-text / HTML editor answers.

CREATE TABLE IF NOT EXISTS faqs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    question VARCHAR(500) NOT NULL,
    answer TEXT NOT NULL,
    display_order INT NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

-- Index for public queries: active FAQs sorted by display_order, created_at, and id.
CREATE INDEX IF NOT EXISTS idx_faqs_public
    ON faqs (display_order ASC, created_at ASC, id ASC)
    WHERE is_deleted = FALSE AND is_active = TRUE;

-- Index for admin queries: all non-deleted FAQs sorted by display_order, created_at, and id.
CREATE INDEX IF NOT EXISTS idx_faqs_admin
    ON faqs (display_order ASC, created_at ASC, id ASC)
    WHERE is_deleted = FALSE;
