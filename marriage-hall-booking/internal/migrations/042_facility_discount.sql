-- A discount shown on the listing: "15% off", struck-through price on the card.
--
-- Stored on the facility rather than derived from coupons: a coupon is a code
-- the customer types at checkout, this is a price the venue advertises. The two
-- coexist - a venue can advertise 15% off and still accept a coupon on top.
--
-- Percent rather than a flat amount: a hall's starting price varies by date and
-- package, so a flat "5000 off" would be wrong for every price but one.
ALTER TABLE facilities ADD COLUMN IF NOT EXISTS discount_percent NUMERIC(5,2)
    CHECK (discount_percent IS NULL OR (discount_percent > 0 AND discount_percent <= 100));

-- An advertised discount that has quietly expired is worse than none: the app
-- keeps showing a price the venue no longer honours. NULL means no end date.
ALTER TABLE facilities ADD COLUMN IF NOT EXISTS discount_valid_until TIMESTAMPTZ;

-- Shown next to the price, e.g. "Monsoon offer". Optional.
ALTER TABLE facilities ADD COLUMN IF NOT EXISTS discount_label VARCHAR(100);
