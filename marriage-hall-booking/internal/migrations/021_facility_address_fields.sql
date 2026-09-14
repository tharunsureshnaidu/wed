-- Align facilities with Java's Facility entity / FacilityDTO.
--
-- The DTO is a client contract: MarriageHallDTO and HotelDTO expose
-- fullAddress, zipcode, lat, lng and vendorId. This table had street/zip_code
-- (different names), and no vendor_id at all, so those fields could never be
-- returned.
--
-- street already holds what Java calls fullAddress - rename rather than add a
-- column and leave the existing rows' address behind.
ALTER TABLE facilities RENAME COLUMN street TO full_address;
ALTER TABLE facilities RENAME COLUMN zip_code TO zipcode;

ALTER TABLE facilities ADD COLUMN IF NOT EXISTS vendor_id UUID REFERENCES vendors(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_facilities_vendor ON facilities (vendor_id) WHERE is_deleted = FALSE;
