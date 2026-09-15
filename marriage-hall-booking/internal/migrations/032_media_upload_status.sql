-- Uploading to S3 takes 1.7-6s, nearly all of it network, and the caller waits
-- for every second of it. Moving the upload to the worker means the row is
-- created immediately and filled in when the object lands, so the listing form
-- returns at once.
--
-- status tracks that: PENDING until the worker uploads, READY once url points
-- at a real object, FAILED when it gave up. A row recorded by URL (the JSON
-- form, where nothing is uploaded) is READY on arrival.
ALTER TABLE facility_images
    ADD COLUMN IF NOT EXISTS status VARCHAR(20) NOT NULL DEFAULT 'READY';
ALTER TABLE facility_videos
    ADD COLUMN IF NOT EXISTS status VARCHAR(20) NOT NULL DEFAULT 'READY';

ALTER TABLE facility_images DROP CONSTRAINT IF EXISTS chk_image_status;
ALTER TABLE facility_images ADD CONSTRAINT chk_image_status
    CHECK (status IN ('PENDING', 'READY', 'FAILED'));
ALTER TABLE facility_videos DROP CONSTRAINT IF EXISTS chk_video_status;
ALTER TABLE facility_videos ADD CONSTRAINT chk_video_status
    CHECK (status IN ('PENDING', 'READY', 'FAILED'));

-- error_message explains a FAILED row without digging through worker logs.
ALTER TABLE facility_images ADD COLUMN IF NOT EXISTS error_message TEXT;
ALTER TABLE facility_videos ADD COLUMN IF NOT EXISTS error_message TEXT;

-- The worker looks rows up by id only, but a partial index on the unfinished
-- ones keeps any "what is still pending" sweep cheap as the table grows.
CREATE INDEX IF NOT EXISTS idx_facility_images_pending
    ON facility_images (created_at) WHERE status = 'PENDING';
CREATE INDEX IF NOT EXISTS idx_facility_videos_pending
    ON facility_videos (created_at) WHERE status = 'PENDING';
