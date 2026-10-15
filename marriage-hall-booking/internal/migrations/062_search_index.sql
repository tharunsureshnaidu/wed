-- Indexed venue search: full-text over name, city, amenities and description,
-- a trigram index for typos, and a review-weighted rating to sort on.
-- See pkg/venuesearch for the queries these serve.

-- What a venue's text search matches, weighted so a name hit outranks a city
-- hit, then an amenity or its type, then a word buried in the description.
-- The type is there as words ("marriage hall venue", "hotel venue") so "hotel
-- bengaluru" finds Grand Residency, and "venue" in a query never sinks it.
--
-- Amenities live in another table, so this cannot be a generated column; the
-- triggers below keep it current from every write path - the owner and admin
-- handlers and cmd/import - rather than trusting each to remember.
ALTER TABLE facilities ADD COLUMN IF NOT EXISTS search_vector TSVECTOR;

CREATE OR REPLACE FUNCTION facility_search_vector(fid UUID, fname TEXT, fcity TEXT, ftype TEXT, fdesc TEXT)
RETURNS TSVECTOR LANGUAGE sql STABLE AS $$
    SELECT setweight(to_tsvector('english', COALESCE(fname, '')), 'A')
        || setweight(to_tsvector('english', COALESCE(fcity, '')), 'B')
        || setweight(to_tsvector('english', replace(lower(COALESCE(ftype, '')), '_', ' ')||' venue'), 'C')
        || setweight(to_tsvector('english', COALESCE((
               SELECT string_agg(a.name, ' ') FROM facility_amenities fa
                 JOIN amenities a ON a.id = fa.amenity_id
                WHERE fa.facility_id = fid), '')), 'C')
        || setweight(to_tsvector('english', COALESCE(fdesc, '')), 'D')
$$;

CREATE OR REPLACE FUNCTION trg_facility_search_vector() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    NEW.search_vector := facility_search_vector(NEW.id, NEW.name, NEW.city, NEW.type, NEW.description);
    RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS facility_search_vector ON facilities;
CREATE TRIGGER facility_search_vector
    BEFORE INSERT OR UPDATE OF name, city, type, description ON facilities
    FOR EACH ROW EXECUTE FUNCTION trg_facility_search_vector();

-- An amenity added to or removed from a venue.
CREATE OR REPLACE FUNCTION trg_facility_amenity_search_vector() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    UPDATE facilities f
       SET search_vector = facility_search_vector(f.id, f.name, f.city, f.type, f.description)
     WHERE f.id = COALESCE(NEW.facility_id, OLD.facility_id);
    RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS facility_amenity_search_vector ON facility_amenities;
CREATE TRIGGER facility_amenity_search_vector
    AFTER INSERT OR DELETE ON facility_amenities
    FOR EACH ROW EXECUTE FUNCTION trg_facility_amenity_search_vector();

-- An amenity renamed: every venue offering it.
CREATE OR REPLACE FUNCTION trg_amenity_search_vector() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    UPDATE facilities f
       SET search_vector = facility_search_vector(f.id, f.name, f.city, f.type, f.description)
     WHERE f.id IN (SELECT facility_id FROM facility_amenities WHERE amenity_id = NEW.id);
    RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS amenity_search_vector ON amenities;
CREATE TRIGGER amenity_search_vector
    AFTER UPDATE OF name ON amenities
    FOR EACH ROW WHEN (OLD.name IS DISTINCT FROM NEW.name)
    EXECUTE FUNCTION trg_amenity_search_vector();

UPDATE facilities SET search_vector = facility_search_vector(id, name, city, type, description);

CREATE INDEX IF NOT EXISTS idx_facilities_search_vector
    ON facilities USING gin (search_vector) WHERE is_deleted = FALSE;

-- The typo prefilter. The expression must stay byte-identical to
-- venuesearch's haystack, or the planner will not match it to this index.
CREATE INDEX IF NOT EXISTS idx_facilities_haystack_trgm
    ON facilities USING gin ((name||' '||COALESCE(city,'')) gin_trgm_ops) WHERE is_deleted = FALSE;

-- Rating weighted by how many reviews back it (a Bayesian average): five
-- phantom reviews at 3.5 are mixed into every venue's score, so 5.0 from one
-- review ranks below 4.6 from 200. An unreviewed venue scores exactly 3.5.
ALTER TABLE facilities ADD COLUMN IF NOT EXISTS rating_score NUMERIC(5,4)
    GENERATED ALWAYS AS (
        (COALESCE(review_count, 0) * COALESCE(avg_rating, 0) + 5 * 3.5)
        / (COALESCE(review_count, 0) + 5)
    ) STORED;

-- The two sorts a browse with no search text pages through in index order.
-- The predicate is venuetype.LiveSQL, so every customer-facing query qualifies.
CREATE INDEX IF NOT EXISTS idx_facilities_live_price
    ON facilities (base_price_per_day) WHERE is_deleted = FALSE AND status = 'APPROVED';
CREATE INDEX IF NOT EXISTS idx_facilities_live_rating
    ON facilities (rating_score DESC) WHERE is_deleted = FALSE AND status = 'APPROVED';
