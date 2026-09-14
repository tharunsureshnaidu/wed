-- A development admin account, so the admin endpoints can be exercised without
-- hand-editing user_roles after every fresh database.
--
--   admin@example.com / Admin@123
--
-- The hash is bcrypt cost 12, the same cost the application uses. It is a known
-- password in a committed file: fine for local work, an open door anywhere
-- real. Change it (or delete the row) before this database faces anyone.
--
-- Created ACTIVE and pre-verified so it can log in immediately - the OTP flow
-- has no meaning for a seeded account.
INSERT INTO users (full_name, email, password_hash, status,
                   is_email_verified, is_phone_verified)
VALUES ('Admin User', 'admin@example.com',
        '$2a$12$pJbsKYfMGwXhzSRcsU1TYeg0aPINjbwOjkluajBuwuEdIxwBVqMfy',
        'ACTIVE', TRUE, TRUE)
ON CONFLICT (email) DO NOTHING;

-- ROLE_ADMIN plus ROLE_CUSTOMER: roles are additive here, and an admin who
-- cannot book or favourite anything is awkward to test with.
INSERT INTO user_roles (user_id, role_id)
SELECT u.id, r.id
  FROM users u
  JOIN roles r ON r.role_name IN ('ROLE_ADMIN', 'ROLE_CUSTOMER')
 WHERE u.email = 'admin@example.com'
ON CONFLICT DO NOTHING;

-- Registration also creates the profile row (see auth's OnUserCreated), and
-- /api/v1/users/me joins it - without this the seeded admin 404s on its own
-- profile. user_profiles.id is the user id, not a separate key.
INSERT INTO user_profiles (id, first_name, last_name)
SELECT u.id, 'Admin', 'User'
  FROM users u WHERE u.email = 'admin@example.com'
ON CONFLICT (id) DO NOTHING;
