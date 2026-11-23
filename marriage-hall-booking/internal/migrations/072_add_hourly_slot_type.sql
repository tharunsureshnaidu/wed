-- 072_add_hourly_slot_type.sql
-- Allow 'HOURLY' as a valid slot_type in hall_availability for hotel hourly bookings.

ALTER TABLE hall_availability DROP CONSTRAINT IF EXISTS hall_availability_slot_type_check;

ALTER TABLE hall_availability ADD CONSTRAINT hall_availability_slot_type_check
    CHECK (slot_type IN ('MORNING', 'EVENING', 'FULL_DAY', 'HOURLY'));
