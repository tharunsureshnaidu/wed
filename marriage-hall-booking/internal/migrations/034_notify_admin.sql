-- Admin gets its own copy of every booking notification, chased until someone
-- on the ops side acknowledges it - same treatment as the owner.
--
-- The destination is an ops address from the environment, not a users row:
-- ROLE_ADMIN is held by dozens of accounts (many of them test users), and
-- fanning every booking out to all of them would bill a real message per admin
-- per channel. One watched inbox is what ops actually wants.
--
-- recipient_user_id stays NULL for these rows: there is no single user behind
-- the ops address, which is also why the ack route authorises by role instead.
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_recipient_role_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_recipient_role_check
    CHECK (recipient_role IN ('OWNER', 'CUSTOMER', 'ADMIN'));
