-- A customer and a vendor are separate entities: one account cannot be both.
--
-- Enforced in the database as well as the handlers, because roles are granted
-- from three places (registration, the admin create-user screen, and the
-- import script) and a rule kept only in Go is one new code path away from
-- being broken silently.
--
-- ADMIN + CUSTOMER is deliberately still allowed: 107 admin accounts hold
-- exactly that pair today, and admin tokens list both roles. The rule being
-- stated here is narrow and specific - CUSTOMER and HALL_OWNER are mutually
-- exclusive - not "one role per user", which would break every one of them.

CREATE OR REPLACE FUNCTION assert_customer_vendor_exclusive() RETURNS TRIGGER AS $$
DECLARE
    granted TEXT;
BEGIN
    SELECT role_name INTO granted FROM roles WHERE id = NEW.role_id;

    IF granted = 'ROLE_CUSTOMER' AND EXISTS (
        SELECT 1 FROM user_roles ur JOIN roles r ON r.id = ur.role_id
         WHERE ur.user_id = NEW.user_id AND r.role_name = 'ROLE_HALL_OWNER'
    ) THEN
        RAISE EXCEPTION 'user % is a vendor and cannot also be a customer', NEW.user_id
            USING ERRCODE = 'check_violation';
    END IF;

    IF granted = 'ROLE_HALL_OWNER' AND EXISTS (
        SELECT 1 FROM user_roles ur JOIN roles r ON r.id = ur.role_id
         WHERE ur.user_id = NEW.user_id AND r.role_name = 'ROLE_CUSTOMER'
    ) THEN
        RAISE EXCEPTION 'user % is a customer and cannot also be a vendor', NEW.user_id
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_customer_vendor_exclusive ON user_roles;
CREATE TRIGGER trg_customer_vendor_exclusive
    BEFORE INSERT OR UPDATE ON user_roles
    FOR EACH ROW EXECUTE FUNCTION assert_customer_vendor_exclusive();
