-- Ported from auth/V6__create_login_attempts_table.sql.
CREATE TABLE login_attempts (
    id BIGSERIAL PRIMARY KEY,
    identifier VARCHAR(255) NOT NULL,
    ip_address VARCHAR(45),
    success BOOLEAN NOT NULL,
    failure_reason VARCHAR(100),
    attempted_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_la_identifier ON login_attempts(identifier);
CREATE INDEX idx_la_ip ON login_attempts(ip_address);
