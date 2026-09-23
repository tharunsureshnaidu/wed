-- Geo-targeted notifications: tell users near a venue about a new listing,
-- new amenities or a new coupon.
--
-- earthdistance over cube, not PostGIS: the only question asked is "is this
-- point within N km of that one", which earth_box answers with an index. PostGIS
-- is a large extension to install and operate for one distance predicate.
CREATE EXTENSION IF NOT EXISTS cube;
CREATE EXTENSION IF NOT EXISTS earthdistance;

-- Users had no location at all, so a radius query had nobody to find.
ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS lat DOUBLE PRECISION;
ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS lng DOUBLE PRECISION;

-- Where the coordinates came from, because the three sources deserve different
-- trust: DEVICE is a GPS fix, CITY is a place the user picked, BOOKING is
-- inferred from venues they have booked and is the weakest. A DEVICE fix must
-- never be overwritten by an inference.
ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS location_source VARCHAR(10)
    CHECK (location_source IN ('DEVICE', 'CITY', 'BOOKING'));
ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS location_updated_at TIMESTAMPTZ;

-- Per-user opt-out. Without it the only way to stop nearby-venue marketing is
-- to unregister every device, which also kills booking notifications.
ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS geo_notifications_enabled BOOLEAN NOT NULL DEFAULT TRUE;

-- The radius query's index. Without it every geo event sequentially scans
-- user_profiles and computes a great-circle distance per row.
--
-- ll_to_earth is IMMUTABLE only on non-null input, so the partial index both
-- keeps it small (most users have no location yet) and makes it valid.
CREATE INDEX IF NOT EXISTS idx_user_profiles_earth
    ON user_profiles USING gist (ll_to_earth(lat, lng))
    WHERE lat IS NOT NULL AND lng IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_facilities_earth
    ON facilities USING gist (ll_to_earth(lat::double precision, lng::double precision))
    WHERE lat IS NOT NULL AND lng IS NOT NULL AND is_deleted = FALSE;
