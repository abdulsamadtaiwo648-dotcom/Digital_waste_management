BEGIN;

-- Keep existing rows untouched, but stop before adding the constraint if
-- legacy unit codes collide after trimming and case-folding.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM property_units
        GROUP BY property_id, lower(btrim(unit_code))
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'Cannot add case-insensitive unit uniqueness: duplicate unit codes exist after trimming and case-folding. Reconcile those units before retrying migration 012.';
    END IF;
END;
$$;

CREATE UNIQUE INDEX IF NOT EXISTS property_units_normalized_code_unique_idx
    ON property_units(property_id, lower(btrim(unit_code)));

COMMIT;
