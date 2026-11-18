-- 069_hotel_hourly_packages.sql
-- Adds optional hourly booking support for hotel facilities with up to 3 configurable packages.

ALTER TABLE facilities ADD COLUMN IF NOT EXISTS hourly_booking_enabled BOOLEAN DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS hotel_hourly_packages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    facility_id UUID NOT NULL REFERENCES facilities(id) ON DELETE CASCADE,
    hours INT NOT NULL CHECK (hours > 0 AND hours <= 24),
    price NUMERIC(12, 2) NOT NULL CHECK (price >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_hotel_hourly_packages_facility_hours UNIQUE (facility_id, hours)
);

CREATE INDEX IF NOT EXISTS idx_hotel_hourly_packages_facility
    ON hotel_hourly_packages(facility_id);

