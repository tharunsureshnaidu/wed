-- A GST or PAN number identifies one business. Java enforces this with a
-- unique column on VendorKyc; here the same numbers could be submitted by any
-- number of vendors, which is how one business claims another's registration -
-- or how a typo silently attaches to the wrong vendor.
--
-- Existing collisions are cleared before the index is built, keeping the
-- earliest claim and blanking the rest back to an un-submitted state. Dropping
-- the number is the safe direction: the vendor is asked to submit again, rather
-- than a later claimant keeping a registration that was never theirs.
WITH dupes AS (
    SELECT id, row_number() OVER (
               PARTITION BY upper(kyc_gst_number) ORDER BY created_at
           ) AS rn
      FROM vendors
     WHERE kyc_gst_number IS NOT NULL AND is_deleted = FALSE
)
UPDATE vendors v
   SET kyc_gst_number = NULL,
       kyc_status = 'PENDING',
       kyc_rejection_reason = 'GST number was already registered to another vendor'
  FROM dupes d
 WHERE v.id = d.id AND d.rn > 1;

WITH dupes AS (
    SELECT id, row_number() OVER (
               PARTITION BY upper(kyc_pan_number) ORDER BY created_at
           ) AS rn
      FROM vendors
     WHERE kyc_pan_number IS NOT NULL AND is_deleted = FALSE
)
UPDATE vendors v
   SET kyc_pan_number = NULL,
       kyc_status = 'PENDING',
       kyc_rejection_reason = 'PAN number was already registered to another vendor'
  FROM dupes d
 WHERE v.id = d.id AND d.rn > 1;

-- Partial, because most vendors have neither filled in yet and NULLs must not
-- collide. Upper-cased so "29abcde1234f1z5" cannot slip past the same value in
-- caps.
CREATE UNIQUE INDEX IF NOT EXISTS uq_vendor_kyc_gst
    ON vendors (upper(kyc_gst_number))
 WHERE kyc_gst_number IS NOT NULL AND is_deleted = FALSE;

CREATE UNIQUE INDEX IF NOT EXISTS uq_vendor_kyc_pan
    ON vendors (upper(kyc_pan_number))
 WHERE kyc_pan_number IS NOT NULL AND is_deleted = FALSE;
