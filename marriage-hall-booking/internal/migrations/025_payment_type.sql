-- Java's PaymentResponse carries paymentType (FULL/ADVANCE/BALANCE). Payments
-- here always charge the booking's outstanding balance, so the type is derived
-- from what is being paid rather than taken from the request: a first payment
-- covering the whole total is FULL, a later one is BALANCE.
ALTER TABLE payments ADD COLUMN IF NOT EXISTS payment_type VARCHAR(20) NOT NULL DEFAULT 'FULL';
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_payment_type_check;
ALTER TABLE payments ADD CONSTRAINT payments_payment_type_check
    CHECK (payment_type IN ('FULL','ADVANCE','BALANCE'));
