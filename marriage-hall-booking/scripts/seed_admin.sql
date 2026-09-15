-- admin@example.com / Admin@123  (ROLE_ADMIN + ROLE_CUSTOMER)
--
-- Migration 026 already does this at start-up; this file is for running by hand
-- against a database where the row is missing or the password has drifted.
--
--   psql -h localhost -U postgres -d venue -f scripts/seed_admin.sql
--
-- The hash is bcrypt cost 12 of Admin@123 - a known password in a committed
-- file. Fine for getting in the first time; replace it before the server faces
-- anyone (see scripts/create_admin.sql).

BEGIN;

-- ACTIVE and pre-verified: an unverified account cannot log in, and the OTP
-- flow has no meaning for a seeded one. ON CONFLICT resets the password, so
-- re-running this recovers a locked-out or altered admin.
INSERT INTO users (full_name, email, password_hash, status,
                   is_email_verified, is_phone_verified)
VALUES ('Admin User', 'admin@example.com',
        '$2a$12$pJbsKYfMGwXhzSRcsU1TYeg0aPINjbwOjkluajBuwuEdIxwBVqMfy',
        'ACTIVE', TRUE, TRUE)
ON CONFLICT (email) DO UPDATE
   SET password_hash     = EXCLUDED.password_hash,
       status            = 'ACTIVE',
       is_email_verified = TRUE,
       failed_login_attempts = 0,
       account_locked_until = NULL,
       is_deleted        = FALSE,
       updated_at        = CURRENT_TIMESTAMP;

-- Roles are additive; ROLE_CUSTOMER too so the account can also book.
INSERT INTO user_roles (user_id, role_id)
SELECT u.id, r.id
  FROM users u
  JOIN roles r ON r.role_name IN ('ROLE_ADMIN', 'ROLE_CUSTOMER')
 WHERE u.email = 'admin@example.com'
ON CONFLICT DO NOTHING;

-- /api/v1/users/me joins user_profiles; without this row the admin 404s on its
-- own profile. user_profiles.id IS the user id.
INSERT INTO user_profiles (id, first_name, last_name)
SELECT u.id, 'Admin', 'User'
  FROM users u WHERE u.email = 'admin@example.com'
ON CONFLICT (id) DO NOTHING;

COMMIT;

SELECT u.id, u.email, u.status,
       string_agg(r.role_name, ', ' ORDER BY r.role_name) AS roles
  FROM users u
  JOIN user_roles ur ON ur.user_id = u.id
  JOIN roles r ON r.id = ur.role_id
 WHERE u.email = 'admin@example.com'
 GROUP BY u.id, u.email, u.status;
