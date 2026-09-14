-- Quotes carry line items and a computed total, and each reply/counter is a new
-- immutable version so the negotiation history survives. Extends the base
-- quotes table from 016.
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS budget_min DECIMAL(10,2);
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS budget_max DECIMAL(10,2);
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS special_requirements TEXT;
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS preferred_contact VARCHAR(20);
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS rejection_reason TEXT;

CREATE TABLE IF NOT EXISTS quote_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    quote_id UUID NOT NULL REFERENCES quotes(id) ON DELETE CASCADE,
    version_no INT NOT NULL,
    -- OWNER replies, CUSTOMER counters.
    created_by_role VARCHAR(20) NOT NULL CHECK (created_by_role IN ('OWNER', 'CUSTOMER')),
    created_by BIGINT NOT NULL REFERENCES users(id),
    items JSONB NOT NULL,
    subtotal DECIMAL(10,2) NOT NULL,
    discount DECIMAL(10,2) NOT NULL DEFAULT 0,
    tax DECIMAL(10,2) NOT NULL DEFAULT 0,
    service_charge DECIMAL(10,2) NOT NULL DEFAULT 0,
    total DECIMAL(10,2) NOT NULL,
    notes TEXT,
    valid_until DATE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_quote_version UNIQUE (quote_id, version_no)
);
CREATE INDEX IF NOT EXISTS idx_quote_versions_quote ON quote_versions(quote_id);

ALTER TABLE quote_messages ADD COLUMN IF NOT EXISTS message_type VARCHAR(20) DEFAULT 'TEXT';

CREATE TABLE IF NOT EXISTS quote_attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    quote_id UUID NOT NULL REFERENCES quotes(id) ON DELETE CASCADE,
    uploaded_by BIGINT NOT NULL REFERENCES users(id),
    file_name VARCHAR(255) NOT NULL,
    file_url VARCHAR(500) NOT NULL,
    content_type VARCHAR(100),
    size_bytes BIGINT,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_quote_attachments_quote ON quote_attachments(quote_id);
