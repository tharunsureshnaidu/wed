-- Java's QuoteRequestDTO carries validUntil (how long the quoted price holds).
-- The column never existed, so the field could not be returned or set.
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS valid_until TIMESTAMPTZ;
