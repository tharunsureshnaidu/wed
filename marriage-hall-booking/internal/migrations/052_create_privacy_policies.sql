-- Privacy Policy table (simple CRUD, single record, no versioning or status).
-- NOTE: Migration 053 drops the deprecated columns if this migration ran
--       with the older versioned schema.

CREATE TABLE IF NOT EXISTS privacy_policies (
    id BIGSERIAL PRIMARY KEY,
    title VARCHAR(255) NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    updated_by BIGINT REFERENCES users(id) ON DELETE SET NULL
);

-- Seed initial Privacy Policy if table is empty (fresh install only).
INSERT INTO privacy_policies (title, content)
SELECT
    'Privacy Policy',
    '<h1>Privacy Policy</h1><p>Welcome to Marriage Hall &amp; Venue Booking System. We value your privacy and are committed to protecting your personal data.</p><h2>1. Information We Collect</h2><p>We collect information to provide better services to all our users:</p><ul><li><strong>Account Information:</strong> Full name, email address, phone number, and password when you register.</li><li><strong>Profile Information:</strong> Optional profile picture, contact preferences, and location details.</li><li><strong>Booking Information:</strong> Marriage hall and hotel reservation details, dates, guest counts, room preferences, and event types.</li><li><strong>Vendor Information:</strong> Business name, tax registration/KYC documents, property addresses, pricing rules, and bank account details for payout processing.</li><li><strong>Payment Information:</strong> Payment status, transaction reference IDs, and payment method details processed via secure payment gateways.</li><li><strong>Reviews &amp; Feedback:</strong> Ratings, reviews, and app feedback submitted by customers.</li><li><strong>Notifications &amp; Device Information:</strong> Device tokens for push notifications, IP addresses, and app usage logs.</li></ul><h2>2. How We Use Information</h2><p>We use the collected information for:</p><ul><li>Processing and managing venue bookings and payments.</li><li>Verifying user identities and facilitating customer-vendor communication.</li><li>Sending booking updates, OTP verifications, and operational notifications.</li><li>Preventing fraud and securing platform operations.</li></ul><h2>3. Data Sharing &amp; Security</h2><p>We do not sell your personal data. We share details only with booked venue owners to fulfill your reservations. All communication is encrypted using standard SSL/TLS protocols.</p><h2>4. Account Deletion &amp; Data Rights</h2><p>Users may request account deletion or data retrieval through customer support. Deleting an account revokes active sessions and soft-deletes profile information in accordance with applicable retention laws.</p><h2>5. Contact Us</h2><p>If you have questions regarding this Privacy Policy, please contact us at support@tripfactory.travel.</p>'
WHERE NOT EXISTS (SELECT 1 FROM privacy_policies);
