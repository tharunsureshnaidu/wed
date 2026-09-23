-- How many guest rooms the customer needs alongside the hall - a wedding party
-- staying overnight, for example.
--
-- A plain count, not priced inventory: hotel bookings already price rooms
-- through booking_rooms against room_types, and marriage halls have no room
-- types defined. This is a number the owner reads and arranges offline, so it
-- deliberately does not touch total_amount.
--
-- Nullable: it is optional, and NULL ("not stated") is different from 0
-- ("explicitly none").
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS room_count INT
    CHECK (room_count IS NULL OR room_count >= 0);
