-- Outbox for owner/customer notifications. A row is created per (booking,
-- recipient, channel) when booking.created is consumed, and retried on an
-- interval until the owner acknowledges - so a booking is never silently
-- missed because one SMS vendor was down.
--
-- The outbox is what makes delivery survive a worker restart: Kafka's offset
-- is committed as soon as the event is handled, so retry state cannot live in
-- the consumer. It also makes the send idempotent - a redelivered Kafka
-- message hits the UNIQUE below instead of sending twice.
CREATE TABLE IF NOT EXISTS notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id UUID NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    -- Who it goes to. OWNER acknowledges; CUSTOMER is informational.
    recipient_role VARCHAR(20) NOT NULL CHECK (recipient_role IN ('OWNER', 'CUSTOMER')),
    recipient_user_id BIGINT REFERENCES users(id),
    channel VARCHAR(20) NOT NULL CHECK (channel IN ('EMAIL', 'SMS', 'WHATSAPP')),
    -- Resolved at enqueue time, not at send time: if the owner edits their
    -- phone number mid-retry the in-flight message still goes where it was
    -- addressed, and the audit trail shows what was actually used.
    destination VARCHAR(255) NOT NULL,
    subject VARCHAR(255),
    body TEXT NOT NULL,

    status VARCHAR(20) NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'SENT', 'ACKED', 'FAILED', 'CANCELLED')),
    attempts INT NOT NULL DEFAULT 0,
    -- NULL means "never send again" (terminal). A due row has next_attempt_at
    -- in the past; the worker claims those.
    next_attempt_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    last_error TEXT,
    sent_at TIMESTAMPTZ,
    acked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,

    -- Idempotency: one row per booking/recipient/channel. A duplicate Kafka
    -- delivery is absorbed by ON CONFLICT DO NOTHING at the insert.
    CONSTRAINT uq_notification_target UNIQUE (booking_id, recipient_role, channel)
);

-- The sweeper's only query: due rows, oldest first. Partial index so it stays
-- small - terminal rows (SENT/ACKED/FAILED) are the vast majority over time
-- and are never scanned.
CREATE INDEX IF NOT EXISTS idx_notifications_due
    ON notifications (next_attempt_at)
    WHERE status = 'PENDING' AND next_attempt_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_notifications_booking ON notifications (booking_id);
