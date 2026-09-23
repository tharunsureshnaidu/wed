-- recipient_user_id had no ON DELETE rule, so a notification row pinned the
-- user it was about: deleting the account failed with a foreign-key violation
-- (found while removing a test user). booking_id already cascades; this makes
-- the user reference behave the same way.
--
-- CASCADE rather than SET NULL: a notification addressed to a user who no
-- longer exists has nobody to deliver to and nothing to audit - the account it
-- described is gone.
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_recipient_user_id_fkey;
ALTER TABLE notifications ADD CONSTRAINT notifications_recipient_user_id_fkey
    FOREIGN KEY (recipient_user_id) REFERENCES users(id) ON DELETE CASCADE;
