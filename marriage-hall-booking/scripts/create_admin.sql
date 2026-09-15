-- Create or promote a super-admin.
--
-- Migration 026 seeds admin@example.com / Admin@123 automatically. That is a
-- known password in a committed file - fine locally, an open door on a public
-- server. Use this to create your own account and then remove the seeded one.
--
--   1. Generate a bcrypt hash (cost 12, what the app uses):
--        htpasswd -bnBC 12 "" 'YourRealPassword' | tr -d ':\n'
--      (dnf install -y httpd-tools)
--
--   2. Run it, substituting the three values:
--        psql -h localhost -U postgres -d venue \
--             -v email="'you@company.com'" \
--             -v name="'Your Name'" \
--             -v hash="'$2y$12$...'" \
--             -f scripts/create_admin.sql

\set ON_ERROR_STOP on

BEGIN;

-- ACTIVE and pre-verified: the OTP flow has no meaning for a seeded account,
-- and an unverified admin cannot log in.
INSERT INTO users (full_name, email, password_hash, status,
                   is_email_verified, is_phone_verified)
VALUES (:name, :email, :hash, 'ACTIVE', TRUE, TRUE)
ON CONFLICT (email) DO UPDATE
   SET password_hash     = EXCLUDED.password_hash,
       status            = 'ACTIVE',
       is_email_verified = TRUE,
       updated_at        = CURRENT_TIMESTAMP;

-- Roles are additive. ROLE_CUSTOMER as well, so the account can book and
-- favourite - an admin that cannot is awkward to test with.
INSERT INTO user_roles (user_id, role_id)
SELECT u.id, r.id
  FROM users u
  JOIN roles r ON r.role_name IN ('ROLE_ADMIN', 'ROLE_CUSTOMER')
 WHERE u.email = :email
ON CONFLICT DO NOTHING;

-- /api/v1/users/me joins user_profiles; without this row the account 404s on
-- its own profile. The id IS the user id, not a separate key.
INSERT INTO user_profiles (id, first_name, last_name)
SELECT u.id, split_part(:name, ' ', 1), NULLIF(split_part(:name, ' ', 2), '')
  FROM users u WHERE u.email = :email
ON CONFLICT (id) DO NOTHING;

COMMIT;

SELECT u.id, u.email, u.status,
       string_agg(r.role_name, ', ' ORDER BY r.role_name) AS roles
  FROM users u
  JOIN user_roles ur ON ur.user_id = u.id
  JOIN roles r ON r.id = ur.role_id
 WHERE u.email = :email
 GROUP BY u.id, u.email, u.status;
