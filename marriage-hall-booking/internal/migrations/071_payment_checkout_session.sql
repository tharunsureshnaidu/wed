-- Migration 071: Add checkout_url and payment_session_id to payments table
ALTER TABLE payments ADD COLUMN IF NOT EXISTS checkout_url TEXT;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS payment_session_id TEXT;
