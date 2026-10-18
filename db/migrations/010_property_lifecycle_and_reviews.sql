BEGIN;

-- Preserve existing addresses, bins, requests, and payment references. A resident
-- can end their relationship with an address without erasing its service history.
ALTER TABLE property_account_memberships
    ADD COLUMN IF NOT EXISTS ended_at timestamptz;

ALTER TABLE property_unit_requests
    ADD COLUMN IF NOT EXISTS review_reason varchar(500);

CREATE OR REPLACE FUNCTION prevent_registered_property_address_edit()
RETURNS trigger AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM registered_properties WHERE location_id = OLD.id)
       AND (NEW.area_id IS DISTINCT FROM OLD.area_id
         OR NEW.label IS DISTINCT FROM OLD.label
         OR NEW.address_line1 IS DISTINCT FROM OLD.address_line1
         OR NEW.address_line2 IS DISTINCT FROM OLD.address_line2
         OR NEW.locality IS DISTINCT FROM OLD.locality
         OR NEW.city IS DISTINCT FROM OLD.city
         OR NEW.state IS DISTINCT FROM OLD.state
         OR NEW.postal_code IS DISTINCT FROM OLD.postal_code
         OR NEW.latitude IS DISTINCT FROM OLD.latitude
         OR NEW.longitude IS DISTINCT FROM OLD.longitude) THEN
        RAISE EXCEPTION 'registered property addresses are immutable; create a new address after moving'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS resident_location_registered_address_immutable ON resident_locations;
CREATE TRIGGER resident_location_registered_address_immutable
    BEFORE UPDATE ON resident_locations
    FOR EACH ROW EXECUTE FUNCTION prevent_registered_property_address_edit();

CREATE INDEX IF NOT EXISTS property_membership_active_user_idx
    ON property_account_memberships(user_id, created_at DESC)
    WHERE ended_at IS NULL;

CREATE INDEX IF NOT EXISTS property_membership_active_unit_idx
    ON property_account_memberships(unit_id, user_id)
    WHERE ended_at IS NULL;

COMMIT;
