-- Admin coupons that apply to every venue of one type - "20% off any marriage
-- hall" - rather than to one vendor or one venue.
--
-- The type is stored, not implied by vendor_id and facility_id both being NULL.
-- That combination used to mean "platform-wide", and validation never looked at
-- the venue's type, so a coupon meant for halls also discounted every hotel.
ALTER TABLE coupons ADD COLUMN IF NOT EXISTS facility_type VARCHAR(30)
    CHECK (facility_type IS NULL OR facility_type IN ('MARRIAGE_HALL', 'HOTEL'));

-- Every coupon is scoped to something: a venue, a vendor's venues, or a venue
-- type. An unscoped row would apply everywhere, which no API creates on purpose.
ALTER TABLE coupons DROP CONSTRAINT IF EXISTS chk_coupons_scope;
ALTER TABLE coupons ADD CONSTRAINT chk_coupons_scope
    CHECK (vendor_id IS NOT NULL OR facility_id IS NOT NULL OR facility_type IS NOT NULL);

CREATE INDEX IF NOT EXISTS idx_coupons_facility_type ON coupons (facility_type)
    WHERE is_deleted = FALSE AND facility_type IS NOT NULL;

-- The coupon a booking redeemed, so cancelling or expiring the booking can give
-- the use back. By id, not by bookings.coupon_code: a deleted coupon's code can
-- be reused, and matching on it would credit the wrong coupon. SET NULL because
-- coupons are soft-deleted; a hard delete must not take bookings with it.
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS coupon_id UUID REFERENCES coupons(id) ON DELETE SET NULL;
