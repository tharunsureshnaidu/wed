-- An admin coupon can now apply to every venue, not just marriage halls.
--
-- The column already allowed MARRIAGE_HALL and HOTEL; the handler only ever
-- wrote MARRIAGE_HALL, so an admin coupon could never reach a hotel. 'ALL' is
-- a third value meaning "any venue, hall or hotel".
--
-- Kept as a value in facility_type rather than a nullable fourth scope, so
-- chk_coupons_scope still guarantees every coupon is scoped to something and
-- no row can silently mean "applies nowhere".

ALTER TABLE coupons DROP CONSTRAINT IF EXISTS coupons_facility_type_check;
ALTER TABLE coupons ADD CONSTRAINT coupons_facility_type_check
    CHECK (facility_type IS NULL
           OR facility_type IN ('MARRIAGE_HALL', 'HOTEL', 'ALL'));
