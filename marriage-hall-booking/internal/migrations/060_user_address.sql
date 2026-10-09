-- Optional address field for users collected during registration.
ALTER TABLE users ADD COLUMN IF NOT EXISTS address VARCHAR(500);
