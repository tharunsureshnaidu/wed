-- Imported venues from the Google Maps scraper.
--
-- Two things the existing schema cannot hold:
--
-- 1. Provenance. Re-running the importer must update the venue it already
--    created rather than inserting a second copy of it, and the Google Maps
--    URL is the only identifier that is stable per place across scrapes.
-- 2. Scraped reviews. The reviews table requires user_id NOT NULL REFERENCES
--    users(id) - a Google reviewer is not a user of this system and inventing
--    an account per reviewer would put unverifiable names into the auth tables.
--    They live in their own table instead, with no FK to users.
ALTER TABLE facilities ADD COLUMN IF NOT EXISTS source VARCHAR(30);
-- TEXT, not VARCHAR(n): this is a URL we do not control the length of. A venue
-- whose name is written in mathematical-bold Unicode percent-encodes to 1132
-- characters, and in Postgres TEXT and VARCHAR are the same storage anyway.
ALTER TABLE facilities ADD COLUMN IF NOT EXISTS source_url TEXT;

-- Google's own place id, which is what actually identifies a venue. The Maps
-- URL is not an identity: the same place is served as both
-- ".../Name/@lat,lng,767m/data=!3m2!..." and ".../Name/data=!4m7!3m6!...",
-- sometimes with a landmark appended to the name in one of them. Keying on the
-- URL imported border-area venues twice, once per city's scrape.
ALTER TABLE facilities ADD COLUMN IF NOT EXISTS source_id VARCHAR(80);

-- Partial unique index, not a column constraint: ordinary venues have a NULL
-- source_id and many of them, so uniqueness applies only to imported rows.
CREATE UNIQUE INDEX IF NOT EXISTS uq_facilities_source_id
    ON facilities (source_id) WHERE source_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS scraped_reviews (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    facility_id UUID NOT NULL REFERENCES facilities(id) ON DELETE CASCADE,
    -- Google's own review id: stable per review, so a re-scrape updates rather
    -- than duplicates.
    external_id VARCHAR(255) NOT NULL,
    author_name VARCHAR(255),
    author_photo TEXT,
    rating INT CHECK (rating BETWEEN 1 AND 5),
    comment TEXT,
    -- Google only ever renders a relative date ("3 months ago"); there is no
    -- absolute timestamp in the page to parse, so it is kept as written.
    relative_date VARCHAR(50),
    source VARCHAR(30) NOT NULL DEFAULT 'GOOGLE_MAPS',
    scraped_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_scraped_review UNIQUE (facility_id, external_id)
);
CREATE INDEX IF NOT EXISTS idx_scraped_reviews_facility ON scraped_reviews(facility_id);

-- Vendors created by the importer, so an imported account is distinguishable
-- from one a real person registered.
ALTER TABLE vendors ADD COLUMN IF NOT EXISTS source VARCHAR(30);

-- Where an imported image came from. The importer copies each Google photo into
-- our own bucket (their URLs expire and block hotlinking), and records the
-- original here so a re-run skips a photo it already copied rather than
-- uploading a second identical object.
ALTER TABLE facility_images ADD COLUMN IF NOT EXISTS source_url TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS uq_facility_images_source
    ON facility_images (facility_id, source_url) WHERE source_url IS NOT NULL;
