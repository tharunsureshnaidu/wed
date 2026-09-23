-- Per-facility contact details and an admin override for the rating.
--
-- Contact previously existed only on vendors (business_phone/business_email),
-- which is one set of details for the whole business. An operator with several
-- halls has a different number on each listing, and an imported venue carries
-- its own contact from the source - neither fits a single vendor-level field.
ALTER TABLE facilities ADD COLUMN IF NOT EXISTS contact_phone VARCHAR(20);
ALTER TABLE facilities ADD COLUMN IF NOT EXISTS contact_email VARCHAR(255);
ALTER TABLE facilities ADD COLUMN IF NOT EXISTS website VARCHAR(255);

-- avg_rating/review_count are normally derived from the reviews table by
-- recalcRating. Imported venues arrive with a rating earned somewhere else and
-- no review rows to compute it from.
--
-- This flag is what stops the next real review from silently wiping that
-- imported number: recalcRating skips a facility whose rating is pinned. It is
-- set only by the admin override endpoint, so an ordinary venue keeps behaving
-- exactly as before.
ALTER TABLE facilities ADD COLUMN IF NOT EXISTS rating_is_manual BOOLEAN NOT NULL DEFAULT FALSE;
