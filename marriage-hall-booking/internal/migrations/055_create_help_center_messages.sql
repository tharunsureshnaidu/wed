-- Help Center / Customer Support messages submitted by authenticated users,
-- and management restricted to SUPER_ADMIN.

-- Ensure ROLE_SUPER_ADMIN exists in the roles catalog.
INSERT INTO roles (role_name) VALUES ('ROLE_SUPER_ADMIN') ON CONFLICT (role_name) DO NOTHING;

-- Grant ROLE_SUPER_ADMIN to the default seeded admin account so local development
-- and super-admin operations are functional out of the box.
INSERT INTO user_roles (user_id, role_id)
SELECT u.id, r.id
  FROM users u, roles r
 WHERE u.email = 'admin@example.com' AND r.role_name = 'ROLE_SUPER_ADMIN'
ON CONFLICT DO NOTHING;

-- Extend notification recipient roles to include SUPER_ADMIN.
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_recipient_role_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_recipient_role_check
    CHECK (recipient_role IN ('OWNER', 'CUSTOMER', 'ADMIN', 'USER', 'VENDOR', 'SUPER_ADMIN'));

-- Help Center messages table.
CREATE TABLE IF NOT EXISTS help_center_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    user_name VARCHAR(100) NOT NULL,
    user_email VARCHAR(255),
    user_phone VARCHAR(50),
    message TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'NEW'
        CHECK (status IN ('NEW', 'READ')),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE
);

-- Super admin triage index: active messages, newest first.
CREATE INDEX IF NOT EXISTS idx_hcm_status_created
    ON help_center_messages (status, created_at DESC) WHERE is_deleted = FALSE;

-- Filter by user: all queries from a given customer.
CREATE INDEX IF NOT EXISTS idx_hcm_user_id
    ON help_center_messages (user_id, created_at DESC) WHERE is_deleted = FALSE;

-- General time-based ordering for listing.
CREATE INDEX IF NOT EXISTS idx_hcm_created_at
    ON help_center_messages (created_at DESC) WHERE is_deleted = FALSE;
