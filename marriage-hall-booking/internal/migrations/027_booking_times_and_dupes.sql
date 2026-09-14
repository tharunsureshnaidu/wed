-- 1. Stop the same venue being listed twice by the same owner.
--
-- Nothing prevented it, so a double-submit (or a retried request) created a
-- second identical listing, which then appears twice in search and in the
-- owner's dashboard.
--
-- Existing duplicates are soft-deleted, newest first, keeping the oldest row -
-- that is the one bookings and quotes already reference.
WITH ranked AS (
    SELECT id, row_number() OVER (
               PARTITION BY owner_id, lower(trim(name))
               ORDER BY created_at
           ) AS rn
      FROM facilities
     WHERE is_deleted = FALSE
)
UPDATE facilities f SET is_deleted = TRUE, updated_at = CURRENT_TIMESTAMP
  FROM ranked r
 WHERE f.id = r.id AND r.rn > 1;

-- Case- and whitespace-insensitive: "Kumar Grand Palace" and "kumar grand
-- palace " are the same venue to a person, and a constraint that disagrees is
-- no protection at all.
CREATE UNIQUE INDEX IF NOT EXISTS uq_facility_owner_name
    ON facilities (owner_id, lower(trim(name)))
 WHERE is_deleted = FALSE;

-- 2. Hall bookings become a date RANGE with times, not one date plus a slot.
--
-- check_in/check_out already exist and are dates; a hall booking simply wrote
-- the same date to both. These add the time-of-day, so an evening reception and
-- a morning function on the same date are two different bookings.
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS start_time TIME;
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS end_time   TIME;
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS guest_count INTEGER;
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS event_type  VARCHAR(30);

-- Availability is per (facility, date, slot). Keeping slot_type as the conflict
-- key means a time range has to map onto a slot: anything covering both halves
-- of the day is FULL_DAY, otherwise MORNING or EVENING by its start time. That
-- keeps the existing conditional-insert conflict check - which is what makes
-- two simultaneous bookings safe - rather than replacing it with range overlap
-- logic that would need its own locking.
CREATE UNIQUE INDEX IF NOT EXISTS uq_hall_availability_slot
    ON hall_availability (facility_id, date, slot_type);
