-- One review per user per facility - but only counting live ones.
--
-- The original UNIQUE (user_id, facility_id) ignored is_deleted, so a
-- soft-deleted review kept occupying the slot forever: a user who deleted their
-- review was told "You have already reviewed this venue" and could never write
-- another. Harmless until the My Reviews screen shipped a delete button, which
-- made it reachable in one tap.
--
-- A partial unique index enforces the same rule where it is meant to apply. The
-- create path's ON CONFLICT / 23505 handling is unchanged: a genuine duplicate
-- still collides.
ALTER TABLE reviews DROP CONSTRAINT IF EXISTS uq_review_user_facility;

CREATE UNIQUE INDEX IF NOT EXISTS uq_review_user_facility_live
    ON reviews (user_id, facility_id) WHERE is_deleted = FALSE;
