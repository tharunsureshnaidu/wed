CREATE TABLE IF NOT EXISTS payment_policies (
    id BIGSERIAL PRIMARY KEY,
    scope VARCHAR(30) NOT NULL CHECK (scope IN ('MARKETPLACE', 'VENUE')),
    facility_id UUID NULL REFERENCES facilities(id) ON DELETE CASCADE,
    advance_percent NUMERIC(5,2) NOT NULL DEFAULT 50,
    min_advance_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    max_advance_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    balance_due_hours_before_event INTEGER NOT NULL DEFAULT 72,
    cancellation_window_hours INTEGER NOT NULL DEFAULT 168,
    cancellation_penalty_percent NUMERIC(5,2) NOT NULL DEFAULT 25,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT payment_policy_scope_facility_unique UNIQUE (scope, facility_id)
);

CREATE INDEX IF NOT EXISTS idx_payment_policies_scope_active
    ON payment_policies(scope, is_active, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_payment_policies_facility_active
    ON payment_policies(facility_id, is_active, updated_at DESC);

INSERT INTO payment_policies (
    scope,
    facility_id,
    advance_percent,
    min_advance_amount,
    max_advance_amount,
    balance_due_hours_before_event,
    cancellation_window_hours,
    cancellation_penalty_percent,
    is_active
) VALUES (
    'MARKETPLACE',
    NULL,
    50,
    0,
    0,
    72,
    168,
    25,
    TRUE
) ON CONFLICT (scope, facility_id) DO NOTHING;
