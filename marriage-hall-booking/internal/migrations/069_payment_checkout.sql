-- Persist Cashfree checkout/session metadata so retries and status checks resolve
-- the same provider order instead of creating duplicate payment records.
ALTER TABLE payments ADD COLUMN IF NOT EXISTS checkout_url TEXT;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS payment_session_id VARCHAR(255);

CREATE INDEX IF NOT EXISTS idx_payments_gateway_status
    ON payments(gateway_order_id, status)
    WHERE gateway_order_id IS NOT NULL;
