CREATE TABLE reviews (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    facility_id UUID NOT NULL REFERENCES facilities(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    booking_id UUID REFERENCES bookings(id),
    rating INT NOT NULL CHECK (rating BETWEEN 1 AND 5),
    comment TEXT,
    is_deleted BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    -- One review per user per facility.
    CONSTRAINT uq_review_user_facility UNIQUE (user_id, facility_id)
);
CREATE INDEX idx_reviews_facility ON reviews(facility_id) WHERE is_deleted = false;

CREATE TABLE quotes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    facility_id UUID NOT NULL REFERENCES facilities(id) ON DELETE CASCADE,
    customer_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_date DATE NOT NULL,
    slot_type VARCHAR(20) DEFAULT 'FULL_DAY',
    guest_count INT,
    event_type VARCHAR(50),
    message TEXT,
    quoted_amount DECIMAL(10,2),
    status VARCHAR(20) NOT NULL DEFAULT 'REQUESTED'
        CHECK (status IN ('REQUESTED', 'REPLIED', 'COUNTERED', 'ACCEPTED', 'REJECTED', 'CANCELLED', 'CONVERTED')),
    booking_id UUID REFERENCES bookings(id),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_quotes_customer ON quotes(customer_id);
CREATE INDEX idx_quotes_facility ON quotes(facility_id);

CREATE TABLE quote_messages (
    id BIGSERIAL PRIMARY KEY,
    quote_id UUID NOT NULL REFERENCES quotes(id) ON DELETE CASCADE,
    sender_id BIGINT NOT NULL REFERENCES users(id),
    message TEXT NOT NULL,
    offered_amount DECIMAL(10,2),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_quote_messages_quote ON quote_messages(quote_id);

CREATE TABLE fraud_reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    target_type VARCHAR(20) NOT NULL CHECK (target_type IN ('VENDOR', 'USER', 'FACILITY')),
    target_id VARCHAR(100) NOT NULL,
    reason TEXT NOT NULL,
    reported_by BIGINT REFERENCES users(id),
    status VARCHAR(30) NOT NULL DEFAULT 'OPEN'
        CHECK (status IN ('OPEN', 'RESOLVED_CLEARED', 'RESOLVED_ACTIONED')),
    resolution_note TEXT,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_fraud_reports_status ON fraud_reports(status);
