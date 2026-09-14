-- Bookings.
--
-- Deliberately NOT partitioned, unlike the Java schema. Partitioning by
-- created_at there forced a composite (id, created_at) primary key, which in turn
-- forced every child table to carry a redundant booking_created_at column purely
-- to satisfy the FK. That is a lot of permanent complexity for a table that will
-- not need partitioning until it is very large; a plain table with an index is
-- correct now and can be partitioned later if volume ever justifies it.
CREATE TABLE bookings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id BIGINT NOT NULL REFERENCES users(id),
    target_type VARCHAR(20) NOT NULL CHECK (target_type IN ('HOTEL', 'HALL')),
    target_id UUID NOT NULL REFERENCES facilities(id),
    check_in DATE NOT NULL,
    check_out DATE NOT NULL,
    slot_type VARCHAR(20),
    total_amount DECIMAL(10,2) NOT NULL CHECK (total_amount >= 0),
    discount_amount DECIMAL(10,2) DEFAULT 0,
    paid_amount DECIMAL(10,2) DEFAULT 0,
    coupon_code VARCHAR(50),
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'CONFIRMED', 'CANCELLED', 'COMPLETED', 'EXPIRED')),
    idempotent_key VARCHAR(100),
    expires_at TIMESTAMPTZ,
    guest_name VARCHAR(100),
    guest_email VARCHAR(100),
    guest_phone VARCHAR(20),
    is_deleted BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_dates CHECK (check_out >= check_in)
);

CREATE INDEX idx_bookings_user ON bookings(user_id) WHERE is_deleted = false;
CREATE INDEX idx_bookings_target ON bookings(target_id, check_in);

-- The idempotency guarantee. A UNIQUE index means two concurrent retries of the
-- same request cannot both create a booking: the loser's INSERT fails and it
-- returns the winner's booking instead.
CREATE UNIQUE INDEX idx_bookings_idempotent ON bookings(idempotent_key)
    WHERE idempotent_key IS NOT NULL;

CREATE TABLE booking_rooms (
    id BIGSERIAL PRIMARY KEY,
    booking_id UUID NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    room_type_id UUID NOT NULL REFERENCES room_types(id),
    quantity INT NOT NULL CHECK (quantity > 0),
    price_at_booking DECIMAL(10,2) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_booking_rooms_booking ON booking_rooms(booking_id);

CREATE TABLE booking_packages (
    id BIGSERIAL PRIMARY KEY,
    booking_id UUID NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    package_id UUID NOT NULL REFERENCES hall_packages(id),
    price_at_booking DECIMAL(10,2) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_booking_packages_booking ON booking_packages(booking_id);

CREATE TABLE booking_addons (
    id BIGSERIAL PRIMARY KEY,
    booking_id UUID NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    addon_id UUID NOT NULL REFERENCES add_on_services(id),
    quantity INT DEFAULT 1 CHECK (quantity > 0),
    price_at_booking DECIMAL(10,2) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_booking_addons_booking ON booking_addons(booking_id);

CREATE TABLE booking_status_history (
    id BIGSERIAL PRIMARY KEY,
    booking_id UUID NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    from_status VARCHAR(20),
    to_status VARCHAR(20) NOT NULL,
    reason VARCHAR(255),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_booking_history_booking ON booking_status_history(booking_id);
