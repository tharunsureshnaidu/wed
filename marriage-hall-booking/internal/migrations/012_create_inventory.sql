-- Room types / hall packages / add-ons / availability.
CREATE TABLE room_types (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    facility_id UUID NOT NULL REFERENCES facilities(id) ON DELETE CASCADE,
    name VARCHAR(150) NOT NULL,
    description TEXT,
    capacity_adults INT NOT NULL,
    capacity_children INT NOT NULL DEFAULT 0,
    base_price_per_night DECIMAL(10,2) NOT NULL,
    total_rooms INT NOT NULL DEFAULT 0,
    is_deleted BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_room_types_facility ON room_types(facility_id) WHERE is_deleted = false;

-- booked_rooms may never exceed total_rooms: the database, not application code,
-- is the last line of defence against overbooking.
CREATE TABLE room_availability (
    id BIGSERIAL PRIMARY KEY,
    room_type_id UUID NOT NULL REFERENCES room_types(id) ON DELETE CASCADE,
    date DATE NOT NULL,
    total_rooms INT NOT NULL,
    booked_rooms INT NOT NULL DEFAULT 0,
    price_override DECIMAL(10,2),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_rooms_available CHECK (total_rooms >= booked_rooms),
    CONSTRAINT chk_booked_non_negative CHECK (booked_rooms >= 0)
);
CREATE UNIQUE INDEX idx_availability_room_date ON room_availability(room_type_id, date);

-- One row per (hall, date, slot). The UNIQUE index is what actually prevents a
-- double booking under concurrency - see the ON CONFLICT insert in the booking
-- repository, which turns a lost race into a clean 409 instead of a 500.
CREATE TABLE hall_availability (
    id BIGSERIAL PRIMARY KEY,
    facility_id UUID NOT NULL REFERENCES facilities(id) ON DELETE CASCADE,
    date DATE NOT NULL,
    slot_type VARCHAR(20) NOT NULL DEFAULT 'FULL_DAY'
        CHECK (slot_type IN ('MORNING', 'EVENING', 'FULL_DAY')),
    status VARCHAR(20) NOT NULL DEFAULT 'AVAILABLE'
        CHECK (status IN ('AVAILABLE', 'BOOKED', 'BLOCKED')),
    booking_id UUID,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX idx_availability_facility_date_slot
    ON hall_availability(facility_id, date, slot_type);

CREATE TABLE hall_packages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    facility_id UUID NOT NULL REFERENCES facilities(id) ON DELETE CASCADE,
    name VARCHAR(150) NOT NULL,
    description TEXT,
    price DECIMAL(10,2) NOT NULL,
    guest_capacity INT,
    includes_catering BOOLEAN DEFAULT FALSE,
    included_services TEXT,
    excluded_services TEXT,
    is_deleted BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_hall_packages_facility ON hall_packages(facility_id) WHERE is_deleted = false;

CREATE TABLE add_on_services (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    facility_id UUID NOT NULL REFERENCES facilities(id) ON DELETE CASCADE,
    name VARCHAR(150) NOT NULL,
    description TEXT,
    price DECIMAL(10,2) NOT NULL,
    unit VARCHAR(30),
    is_deleted BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_addons_facility ON add_on_services(facility_id) WHERE is_deleted = false;

CREATE TABLE token_advance_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    facility_id UUID NOT NULL REFERENCES facilities(id) ON DELETE CASCADE,
    min_percentage DECIMAL(5,2) NOT NULL,
    refundable BOOLEAN DEFAULT FALSE,
    refund_window_days INT DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_advance_rules_facility ON token_advance_rules(facility_id);

CREATE TABLE cancellation_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    facility_id UUID NOT NULL REFERENCES facilities(id) ON DELETE CASCADE,
    policy_type VARCHAR(50) NOT NULL,
    days_before_checkin INT NOT NULL,
    refund_percentage DECIMAL(5,2) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
