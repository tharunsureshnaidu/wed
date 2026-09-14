-- Ported from search/V1. Feeds trending queries and popular cities.
CREATE TABLE IF NOT EXISTS search_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id VARCHAR(100),
    query_text VARCHAR(200),
    filters_json TEXT,
    result_count INT NOT NULL,
    clicked_venue_id UUID,
    time_taken_ms INT,
    city VARCHAR(100),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_search_events_created_at ON search_events(created_at);
CREATE INDEX IF NOT EXISTS idx_search_events_city ON search_events(city);

-- Trigram index so ILIKE '%term%' on venue names uses an index instead of
-- scanning every row once the table grows.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_facilities_name_trgm ON facilities USING gin (name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_facilities_city_trgm ON facilities USING gin (city gin_trgm_ops);
