-- Align columns with the request shapes the Postman collection (and the Java
-- DTOs) actually send.
ALTER TABLE add_on_services ADD COLUMN IF NOT EXISTS service_type VARCHAR(50);

ALTER TABLE token_advance_rules ADD COLUMN IF NOT EXISTS advance_percentage DECIMAL(5,2);
ALTER TABLE token_advance_rules ADD COLUMN IF NOT EXISTS min_advance_amount DECIMAL(10,2);
ALTER TABLE token_advance_rules ADD COLUMN IF NOT EXISTS balance_due_days_before INT;
ALTER TABLE token_advance_rules ADD COLUMN IF NOT EXISTS auto_reminder_enabled BOOLEAN DEFAULT FALSE;
-- min_percentage was the original column name; advance_percentage supersedes it.
ALTER TABLE token_advance_rules ALTER COLUMN min_percentage DROP NOT NULL;

-- One active rule per hall: the API upserts rather than accumulating rules.
CREATE UNIQUE INDEX IF NOT EXISTS uq_advance_rule_facility
    ON token_advance_rules(facility_id);
