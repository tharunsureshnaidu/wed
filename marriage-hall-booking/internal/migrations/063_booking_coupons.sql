-- Allow multiple coupons (e.g. 1 Admin coupon + 1 Hall Owner coupon) on a booking.
CREATE TABLE IF NOT EXISTS booking_coupons (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id UUID NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    coupon_id UUID NOT NULL REFERENCES coupons(id) ON DELETE RESTRICT,
    coupon_code VARCHAR(50) NOT NULL,
    coupon_type VARCHAR(20) NOT NULL, -- 'ADMIN' or 'VENDOR'
    discount_amount DECIMAL(10,2) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (booking_id, coupon_id)
);

CREATE INDEX IF NOT EXISTS idx_booking_coupons_booking_id ON booking_coupons(booking_id);
CREATE INDEX IF NOT EXISTS idx_booking_coupons_coupon_id ON booking_coupons(coupon_id);
