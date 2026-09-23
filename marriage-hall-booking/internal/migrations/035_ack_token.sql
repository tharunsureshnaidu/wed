-- One-tap acknowledge link, carried in the notification itself.
--
-- An owner reading an SMS on their phone has no session and no app; asking
-- them to log in to stop a reminder means the reminder never stops. The token
-- IS the authorisation, so it is treated like a credential:
--
--   * random (crypto/rand), not derived from the booking id - guessing one
--     must not be possible from a booking reference that appears in emails.
--   * per row, so a leaked owner link cannot silence the ops copy and a
--     forwarded message only acknowledges what its recipient was sent.
--   * expiring, so an old message in an inbox is not a permanent key.
--
-- Only rows that are chased (OWNER, ADMIN) get a token; a customer's copy has
-- nothing to acknowledge.
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS ack_token VARCHAR(64);
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS ack_token_expires_at TIMESTAMPTZ;

-- Unique so a token resolves to exactly one row. Partial: most rows have none.
CREATE UNIQUE INDEX IF NOT EXISTS uq_notifications_ack_token
    ON notifications (ack_token) WHERE ack_token IS NOT NULL;
