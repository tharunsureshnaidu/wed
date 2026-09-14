-- Vendor business profile, KYC and payout details.
CREATE TABLE vendors (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id BIGINT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    business_name VARCHAR(200) NOT NULL,
    business_address TEXT,
    business_description TEXT,
    business_type VARCHAR(50),
    business_type_other VARCHAR(100),
    kyc_status VARCHAR(20) NOT NULL DEFAULT 'PENDING'
        CHECK (kyc_status IN ('PENDING', 'SUBMITTED', 'APPROVED', 'REJECTED')),
    kyc_document_url VARCHAR(500),
    kyc_rejection_reason VARCHAR(255),
    -- Account numbers are PII; stored masked, never in full.
    bank_account_masked VARCHAR(30),
    bank_ifsc VARCHAR(20),
    bank_holder_name VARCHAR(150),
    is_deleted BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_vendors_kyc ON vendors(kyc_status) WHERE is_deleted = false;
