-- Ported from auth/V2__create_otp_verification_table.sql.
-- otp_code holds a SHA-256 hash of the 6-digit code, never the code itself.
CREATE TABLE otp_verification (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT,
    otp_code VARCHAR(255) NOT NULL,
    otp_type VARCHAR(30) NOT NULL
        CHECK (otp_type IN ('EMAIL_VERIFICATION', 'PHONE_VERIFICATION', 'PASSWORD_RESET', 'LOGIN_2FA')),
    target VARCHAR(255) NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    verified BOOLEAN DEFAULT FALSE,
    attempt_count INT DEFAULT 0,
    ip_address VARCHAR(45),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_otp_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX idx_otp_target ON otp_verification(target);
CREATE INDEX idx_otp_user_id ON otp_verification(user_id);
