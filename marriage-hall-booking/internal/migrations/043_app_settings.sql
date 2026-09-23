-- Admin-editable application settings, starting with the support helpline.
--
-- Key/value rather than a column per setting: the helpline is the first of
-- these, and the next one (terms URL, support hours, a policy blurb) should be
-- a new row, not a new migration and a new deploy.
--
-- is_public decides what the unauthenticated GET returns. A setting is private
-- until someone says otherwise, so adding an internal key later cannot leak it
-- to every app user by accident.
CREATE TABLE IF NOT EXISTS app_settings (
    key VARCHAR(64) PRIMARY KEY,
    value TEXT,
    -- Shown beside the field in the admin UI so an editor knows what they are
    -- changing without reading the code.
    description VARCHAR(255),
    is_public BOOLEAN NOT NULL DEFAULT FALSE,
    -- Who changed it last. SET NULL rather than CASCADE: deleting an admin
    -- must not delete the helpline they once edited.
    updated_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

-- Seeded so the public GET returns a usable shape from the first request
-- rather than an empty object the app has to special-case. The values are
-- placeholders and are meant to be edited.
INSERT INTO app_settings (key, value, description, is_public) VALUES
    ('support.email',    'support@tripfactory.travel', 'Helpline email address shown in the app', TRUE),
    ('support.phone',    '+91 80 4000 0000',           'Helpline phone number shown in the app', TRUE),
    ('support.whatsapp', '',                           'WhatsApp number for support, blank to hide', TRUE),
    ('support.hours',    'Mon-Sat, 9:00 AM - 7:00 PM', 'When the helpline is staffed', TRUE),
    ('support.address',  '',                           'Office address, blank to hide', TRUE)
ON CONFLICT (key) DO NOTHING;
