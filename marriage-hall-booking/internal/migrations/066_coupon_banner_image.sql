-- Banner image for coupons
ALTER TABLE coupons ADD COLUMN IF NOT EXISTS banner_image_url TEXT;

