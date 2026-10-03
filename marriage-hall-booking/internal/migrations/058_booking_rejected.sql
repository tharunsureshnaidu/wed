-- A venue owner can now turn a booking request down.
--
-- REJECTED rather than reusing CANCELLED: the two are different events with
-- different consequences. A customer cancelling is their own choice; a venue
-- rejecting is a refusal the customer did not ask for, needs a reason, and is
-- the number an owner is measured on. Folding them together would make
-- "rejection rate" unanswerable - the decision analytics already counts them
-- separately.

ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_status_check;
ALTER TABLE bookings ADD CONSTRAINT bookings_status_check
    CHECK (status IN ('PENDING', 'CONFIRMED', 'REJECTED',
                      'CANCELLED', 'COMPLETED', 'EXPIRED'));
