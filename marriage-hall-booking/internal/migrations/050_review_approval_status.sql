-- Migration 050: Add review approval status column
ALTER TABLE reviews
ADD COLUMN IF NOT EXISTS status VARCHAR(20) NOT NULL DEFAULT 'PENDING'
CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED'));

-- Set existing reviews to APPROVED so historical reviews remain public and valid
UPDATE reviews SET status = 'APPROVED' WHERE status IS NULL OR status = 'PENDING';

-- Optimization index for public facility reviews list
CREATE INDEX IF NOT EXISTS idx_reviews_facility_approved
ON reviews(facility_id) WHERE is_deleted = false AND status = 'APPROVED';

-- Optimization index for admin filtering by status
CREATE INDEX IF NOT EXISTS idx_reviews_status
ON reviews(status) WHERE is_deleted = false;
