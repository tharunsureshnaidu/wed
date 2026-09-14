-- Align vendors with Java's Vendor entity / VendorResponse.
--
-- VendorResponse exposes the business contact and branding fields plus a
-- vendor-level status; none of them had a column, so GET /vendors/me could
-- never return them.
ALTER TABLE vendors ADD COLUMN IF NOT EXISTS status VARCHAR(20) NOT NULL DEFAULT 'PENDING';
ALTER TABLE vendors ADD COLUMN IF NOT EXISTS business_logo_url VARCHAR(500);
ALTER TABLE vendors ADD COLUMN IF NOT EXISTS cover_image_url VARCHAR(500);
ALTER TABLE vendors ADD COLUMN IF NOT EXISTS business_phone VARCHAR(20);
ALTER TABLE vendors ADD COLUMN IF NOT EXISTS business_email VARCHAR(255);
ALTER TABLE vendors ADD COLUMN IF NOT EXISTS website VARCHAR(500);
ALTER TABLE vendors ADD COLUMN IF NOT EXISTS upi_id VARCHAR(100);
ALTER TABLE vendors ADD COLUMN IF NOT EXISTS business_registration_number VARCHAR(100);
ALTER TABLE vendors ADD COLUMN IF NOT EXISTS support_contact VARCHAR(255);

-- Java models KYC as its own entity with its own business name/address, which
-- may differ from the vendor's display ones (the KYC pair is what was legally
-- filed). kyc_status/kyc_rejection_reason already exist on vendors; these two
-- complete the VendorResponse.KycDetails block.
ALTER TABLE vendors ADD COLUMN IF NOT EXISTS kyc_business_name VARCHAR(255);
ALTER TABLE vendors ADD COLUMN IF NOT EXISTS kyc_business_address TEXT;
ALTER TABLE vendors ADD COLUMN IF NOT EXISTS kyc_gst_number VARCHAR(50);
ALTER TABLE vendors ADD COLUMN IF NOT EXISTS kyc_pan_number VARCHAR(50);

ALTER TABLE vendors DROP CONSTRAINT IF EXISTS vendors_status_check;
ALTER TABLE vendors ADD CONSTRAINT vendors_status_check
    CHECK (status IN ('PENDING','APPROVED','REJECTED','SUSPENDED'));
