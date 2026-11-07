-- Migration 064: Add unique reference_id (Booking Reference ID / hash ID) to bookings.
--
-- Customers receive this unique reference ID upon booking (e.g. BK-A1B2C3D4E5)
-- and can use it when contacting customer support or filing complaints.
-- Admins and venue owners can use it to retrieve booking details.

ALTER TABLE bookings ADD COLUMN IF NOT EXISTS reference_id VARCHAR(50);

-- Backfill existing rows with deterministic hash IDs based on UUID
UPDATE bookings
   SET reference_id = 'BK-' || UPPER(SUBSTRING(md5(id::text) FROM 1 FOR 10))
 WHERE reference_id IS NULL;

-- Default for future inserts
ALTER TABLE bookings ALTER COLUMN reference_id SET DEFAULT ('BK-' || UPPER(SUBSTRING(md5(gen_random_uuid()::text) FROM 1 FOR 10)));
ALTER TABLE bookings ALTER COLUMN reference_id SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_bookings_reference_id ON bookings (reference_id);
