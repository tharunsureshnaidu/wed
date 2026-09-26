-- Which events a venue can host.
--
-- The catalogue itself lives in Go (pkg/events or internal/facility), not in a
-- table: the list changes with a release, not at runtime, and a code column
-- here would need a FK to a table nobody edits. What varies per venue is the
-- selection, which is what this table holds.
--
-- event_code is the catalogue code (WEDDING, RECEPTION, ...), validated in the
-- handler against the Go list. Storing the code rather than an id means a
-- booking's event_type - already a free-text VARCHAR carrying 'WEDDING' - can
-- be compared directly without a join.
CREATE TABLE IF NOT EXISTS facility_events (
    facility_id UUID NOT NULL REFERENCES facilities(id) ON DELETE CASCADE,
    event_code VARCHAR(50) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (facility_id, event_code)
);

-- The search filter's only query: venues hosting one event.
CREATE INDEX IF NOT EXISTS idx_facility_events_code
    ON facility_events (event_code);

-- Every marriage hall hosts weddings - that is what makes it a marriage hall.
-- Seeded so the filter is not empty on day one and so existing venues do not
-- silently stop matching the one event type bookings actually use.
INSERT INTO facility_events (facility_id, event_code)
SELECT id, 'WEDDING' FROM facilities
 WHERE type = 'MARRIAGE_HALL' AND is_deleted = FALSE
ON CONFLICT DO NOTHING;

-- A venue that has already hosted an event type can clearly host it. Recovers
-- the real data rather than asking owners to re-enter what the bookings table
-- already knows.
INSERT INTO facility_events (facility_id, event_code)
SELECT DISTINCT b.target_id, UPPER(TRIM(b.event_type))
  FROM bookings b
 WHERE b.event_type IS NOT NULL AND TRIM(b.event_type) <> ''
   AND b.target_type = 'HALL'
   AND EXISTS (SELECT 1 FROM facilities f WHERE f.id = b.target_id)
ON CONFLICT DO NOTHING;
