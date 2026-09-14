-- Ported from auth/V8__seed_default_roles.sql.
-- ON CONFLICT so a re-run against a partially seeded DB is harmless.
INSERT INTO roles (role_name) VALUES
    ('ROLE_CUSTOMER'),
    ('ROLE_HALL_OWNER'),
    ('ROLE_ADMIN'),
    ('ROLE_STAFF')
ON CONFLICT (role_name) DO NOTHING;
