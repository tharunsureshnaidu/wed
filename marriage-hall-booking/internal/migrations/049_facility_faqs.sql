-- Questions a venue answers on its own detail page: parking, outside catering,
-- alcohol, decoration timings. Per-venue rather than a shared list, because
-- "is outside catering allowed" has a different answer at every hall.
CREATE TABLE IF NOT EXISTS facility_faqs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    facility_id UUID NOT NULL REFERENCES facilities(id) ON DELETE CASCADE,
    question VARCHAR(500) NOT NULL,
    answer TEXT NOT NULL,
    -- The owner's ordering. Ties break on created_at so the list is stable
    -- when every row is left at the default 0.
    sort_order INT NOT NULL DEFAULT 0,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

-- The detail page's only query: one venue's live FAQs in display order.
CREATE INDEX IF NOT EXISTS idx_facility_faqs_facility
    ON facility_faqs (facility_id, sort_order, created_at)
    WHERE is_deleted = FALSE;
