-- Dedicated columns on bookings for Admin and Vendor coupons
ALTER TABLE bookings
    ADD COLUMN IF NOT EXISTS admin_coupon_id UUID REFERENCES coupons(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS admin_coupon_discount DECIMAL(10,2) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS vendor_coupon_id UUID REFERENCES coupons(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS vendor_coupon_discount DECIMAL(10,2) NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_bookings_admin_coupon_id ON bookings(admin_coupon_id) WHERE admin_coupon_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_bookings_vendor_coupon_id ON bookings(vendor_coupon_id) WHERE vendor_coupon_id IS NOT NULL;
