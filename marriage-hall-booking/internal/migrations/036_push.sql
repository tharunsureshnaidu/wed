-- Push notifications, and the triggers beyond booking.created that feed them.
--
-- PUSH becomes a fourth channel on the existing outbox rather than a parallel
-- pipeline: the retry, dedupe and acknowledge machinery is already built and
-- tested, and a push that silently fails is no more acceptable than an SMS
-- that does.
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_channel_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_channel_check
    CHECK (channel IN ('EMAIL', 'SMS', 'WHATSAPP', 'PUSH'));

-- USER is the plain customer being told about something that is not their own
-- booking - their review posted, their account unblocked. Distinct from
-- CUSTOMER, which means "the customer side of this booking".
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_recipient_role_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_recipient_role_check
    CHECK (recipient_role IN ('OWNER', 'CUSTOMER', 'ADMIN', 'USER', 'VENDOR'));

-- DECLINED is the owner actively refusing a booking, as opposed to ACKED
-- (accepted) or CANCELLED (the booking itself went away). Kept distinct so ops
-- can tell "the owner said no" from "nobody ever answered".
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_status_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_status_check
    CHECK (status IN ('PENDING', 'SENT', 'ACKED', 'FAILED', 'CANCELLED', 'DECLINED'));

-- What happened, so the app can group, filter and deep-link. The outbox
-- previously only ever carried booking.created, where the event was implicit.
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS event_type VARCHAR(50);

-- booking_id was NOT NULL because every notification used to be about a
-- booking. Facility approvals, KYC decisions and blocks are not.
ALTER TABLE notifications ALTER COLUMN booking_id DROP NOT NULL;

-- The uniqueness rule that makes redelivery idempotent was (booking_id, role,
-- channel). With booking_id nullable that no longer covers the new events -
-- NULLs never collide in a UNIQUE constraint, so every retry of a facility
-- approval would insert a fresh row. Key on the subject instead: one
-- notification per (event, subject, recipient, channel).
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS subject_id VARCHAR(64);

UPDATE notifications SET subject_id = booking_id::text WHERE subject_id IS NULL;
UPDATE notifications SET event_type = 'booking.created' WHERE event_type IS NULL;

ALTER TABLE notifications DROP CONSTRAINT IF EXISTS uq_notification_target;
CREATE UNIQUE INDEX IF NOT EXISTS uq_notification_event_target
    ON notifications (event_type, subject_id, recipient_role, channel);

-- One row per device, not per user: a vendor with a phone and a tablet must be
-- reached on both, and a token belongs to an install rather than a person.
CREATE TABLE IF NOT EXISTS device_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- UNIQUE so re-registering the same install is an upsert. FCM tokens can
    -- migrate between users on a shared device, which the upsert handles by
    -- reassigning user_id rather than leaving a stale duplicate.
    token VARCHAR(255) NOT NULL UNIQUE,
    platform VARCHAR(10) NOT NULL CHECK (platform IN ('ANDROID', 'IOS', 'WEB')),
    -- Set false when FCM reports the token is dead. Retrying an uninstalled
    -- app burns every attempt in the retry budget and never succeeds.
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    last_seen_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

-- The dispatcher's only query: live tokens for one user.
CREATE INDEX IF NOT EXISTS idx_device_tokens_user
    ON device_tokens (user_id) WHERE is_active;
