-- Settled end-state of the Java facility schema (V1 through V15 collapsed into one
-- table definition - the 15-file migration history is churn this build has no need
-- to replay, since there is no existing data to migrate forward).
CREATE TABLE facilities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    type VARCHAR(30) NOT NULL CHECK (type IN ('HOTEL', 'MARRIAGE_HALL')),
    city VARCHAR(100),
    street VARCHAR(255),
    state VARCHAR(100),
    zip_code VARCHAR(20),
    country VARCHAR(100),
    lat DECIMAL(10,8),
    lng DECIMAL(11,8),
    status VARCHAR(20) DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED', 'BLOCKED')),
    is_verified BOOLEAN DEFAULT FALSE,
    is_featured BOOLEAN DEFAULT FALSE,
    avg_rating NUMERIC(3,2) DEFAULT 0,
    review_count INT DEFAULT 0,
    is_deleted BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,

    -- Hotel-specific
    star_rating INT CHECK (star_rating BETWEEN 1 AND 5),
    check_in_time VARCHAR(10) DEFAULT '14:00',
    check_out_time VARCHAR(10) DEFAULT '11:00',

    -- Marriage-hall-specific
    capacity_pax INT,
    area_sqft INT,
    base_price_per_day DECIMAL(10,2),
    seating_capacity INT,
    floating_capacity INT,
    min_booking_size INT
);

CREATE INDEX idx_facilities_owner ON facilities(owner_id) WHERE is_deleted = false;
CREATE INDEX idx_facilities_type_city ON facilities(type, city) WHERE is_deleted = false;

CREATE TABLE facility_images (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    facility_id UUID NOT NULL REFERENCES facilities(id) ON DELETE CASCADE,
    url VARCHAR(500) NOT NULL,
    is_cover BOOLEAN DEFAULT FALSE,
    sort_order INT DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_facility_images_facility ON facility_images(facility_id);

CREATE TABLE facility_videos (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    facility_id UUID NOT NULL REFERENCES facilities(id) ON DELETE CASCADE,
    url VARCHAR(500) NOT NULL,
    thumbnail_url VARCHAR(500),
    duration_seconds INT,
    sort_order INT DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_facility_videos_facility ON facility_videos(facility_id);

CREATE TABLE amenities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) UNIQUE NOT NULL,
    code VARCHAR(50),
    icon VARCHAR(50),
    applicable_type VARCHAR(20) NOT NULL DEFAULT 'BOTH'
        CHECK (applicable_type IN ('HOTEL', 'MARRIAGE_HALL', 'BOTH')),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE facility_amenities (
    facility_id UUID NOT NULL REFERENCES facilities(id) ON DELETE CASCADE,
    amenity_id UUID NOT NULL REFERENCES amenities(id) ON DELETE CASCADE,
    PRIMARY KEY (facility_id, amenity_id)
);

CREATE TABLE facility_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    facility_id UUID NOT NULL REFERENCES facilities(id) ON DELETE CASCADE,
    policy_type VARCHAR(30) NOT NULL,
    description TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_facility_policies_facility ON facility_policies(facility_id);

CREATE TABLE facility_pricing_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    facility_id UUID NOT NULL REFERENCES facilities(id) ON DELETE CASCADE,
    event_type VARCHAR(50) NOT NULL,
    day_type VARCHAR(20),
    season VARCHAR(50),
    min_guests INT,
    max_guests INT,
    price DECIMAL(10,2) NOT NULL,
    valid_from DATE,
    valid_to DATE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_facility_pricing_facility ON facility_pricing_rules(facility_id);

CREATE TABLE favourite_facilities (
    id BIGSERIAL PRIMARY KEY,
    user_profile_id BIGINT NOT NULL REFERENCES user_profiles(id) ON DELETE CASCADE,
    facility_id UUID NOT NULL REFERENCES facilities(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_user_facility UNIQUE (user_profile_id, facility_id)
);
