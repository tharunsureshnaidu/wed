-- Quotes carried a single event_date, so a two-day wedding could not be quoted
-- at all - and since an accepted quote converts straight into a booking, which
-- has taken a date range since 027, the quote was the narrower of the two.
--
-- end_date is nullable and read as "same as event_date" when absent, so every
-- existing single-day quote stays valid without a backfill.
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS end_date   DATE;
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS start_time VARCHAR(5);
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS end_time   VARCHAR(5);

-- A range that ends before it starts is not a typo to be fixed later: it prices
-- and reserves the wrong number of days. Reject it at the boundary, as
-- bookings.chk_dates already does.
ALTER TABLE quotes DROP CONSTRAINT IF EXISTS chk_quote_dates;
ALTER TABLE quotes ADD CONSTRAINT chk_quote_dates
    CHECK (end_date IS NULL OR end_date >= event_date);
