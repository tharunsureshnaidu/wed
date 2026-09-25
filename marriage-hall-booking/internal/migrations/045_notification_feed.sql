-- The in-app notification feed: the list a user pulls down in the app, as
-- distinct from the outbox rows that deliver by email, SMS and push.
--
-- read_at is a new column rather than a reuse of status. The two answer
-- different questions: status is delivery ("did the SMS leave"), read_at is
-- attention ("did the person look"). A push can be SENT for days and still
-- unread, and marking it read must not make the retry machinery think it was
-- acknowledged.
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS read_at TIMESTAMPTZ;

-- The feed's only query: one user's rows, newest first. Ordering is in the
-- index so the unread count and the first page never sort at runtime.
CREATE INDEX IF NOT EXISTS idx_notifications_feed
    ON notifications (recipient_user_id, created_at DESC)
    WHERE recipient_user_id IS NOT NULL;

-- Partial index for the unread badge, which is read on every app open and is
-- the one query that must stay cheap as the table grows.
CREATE INDEX IF NOT EXISTS idx_notifications_unread
    ON notifications (recipient_user_id)
    WHERE recipient_user_id IS NOT NULL AND read_at IS NULL;
