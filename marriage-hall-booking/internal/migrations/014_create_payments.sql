-- Payments and refunds. Money columns are NUMERIC, never floating point.
CREATE TABLE payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id UUID NOT NULL REFERENCES bookings(id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    amount DECIMAL(10,2) NOT NULL CHECK (amount > 0),
    currency VARCHAR(3) NOT NULL DEFAULT 'INR',
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'SUCCESS', 'FAILED', 'REFUNDED', 'PARTIALLY_REFUNDED')),
    gateway VARCHAR(30) NOT NULL DEFAULT 'MOCK',
    gateway_order_id VARCHAR(120),
    gateway_payment_id VARCHAR(120),
    failure_reason VARCHAR(255),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_payments_booking ON payments(booking_id);
CREATE INDEX idx_payments_user ON payments(user_id);

-- A gateway may deliver the same webhook many times; this makes replays no-ops.
CREATE UNIQUE INDEX idx_payments_gateway_order ON payments(gateway_order_id)
    WHERE gateway_order_id IS NOT NULL;

CREATE TABLE refunds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_id UUID NOT NULL REFERENCES payments(id),
    amount DECIMAL(10,2) NOT NULL CHECK (amount > 0),
    reason VARCHAR(255),
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'SUCCESS', 'FAILED')),
    gateway_refund_id VARCHAR(120),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_refunds_payment ON refunds(payment_id);

-- Every webhook the gateway sends, recorded before it is acted on. The UNIQUE
-- event_id is what makes replayed deliveries idempotent.
CREATE TABLE payment_webhook_events (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(120) NOT NULL UNIQUE,
    payload JSONB NOT NULL,
    processed BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
