-- An absent idempotency key was stored as '' rather than NULL, so the unique
-- index treated two unrelated bookings as the same request: the second caller
-- who omitted the key got 409 IDEMPOTENCY_KEY_CONFLICT against a stranger's
-- booking. Inserts now write NULL (see repository.go); this clears the rows
-- written before that.
UPDATE bookings SET idempotent_key = NULL WHERE idempotent_key = '';
