-- Java's ReviewDTO carries a title alongside the comment; the column was never
-- created, so the field could not be stored or returned.
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS title VARCHAR(200);
