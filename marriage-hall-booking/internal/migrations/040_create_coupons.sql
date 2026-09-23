-- Coupons, created by a vendor for their own venues or by an admin
-- platform-wide.
CREATE TABLE IF NOT EXISTS coupons (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(50) NOT NULL,
    description TEXT,

    -- NULL means platform-wide (admin-created). Non-null scopes the coupon to
    -- one vendor's listings.
    vendor_id UUID REFERENCES vendors(id) ON DELETE CASCADE,
    -- Optional further scoping to a single venue, which is also the point the
    -- "coupon near you" announcement is measured from.
    facility_id UUID REFERENCES facilities(id) ON DELETE CASCADE,

    discount_type VARCHAR(20) NOT NULL CHECK (discount_type IN ('PERCENT', 'FLAT')),
    discount_value DECIMAL(10,2) NOT NULL CHECK (discount_value > 0),
    -- Caps a PERCENT coupon. Without it "50% off" on a large booking is
    -- unbounded exposure.
    max_discount DECIMAL(10,2),
    min_booking_amount DECIMAL(10,2),

    valid_from TIMESTAMPTZ,
    valid_until TIMESTAMPTZ,
    -- NULL means unlimited. used_count is incremented inside the booking
    -- transaction, so a limit of 100 cannot be exceeded by concurrent bookings.
    usage_limit INT,
    used_count INT NOT NULL DEFAULT 0,

    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

-- Codes are typed by customers, so they are matched case-insensitively and
-- must be unique that way. Partial, so a deleted coupon's code can be reused.
CREATE UNIQUE INDEX IF NOT EXISTS uq_coupons_code
    ON coupons (upper(code)) WHERE is_deleted = FALSE;

CREATE INDEX IF NOT EXISTS idx_coupons_vendor ON coupons (vendor_id) WHERE is_deleted = FALSE;
CREATE INDEX IF NOT EXISTS idx_coupons_facility ON coupons (facility_id) WHERE is_deleted = FALSE;
