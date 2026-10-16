BEGIN;

CREATE TABLE IF NOT EXISTS service_areas (
    id bigserial PRIMARY KEY,
    locality varchar(120) NOT NULL,
    city varchar(120) NOT NULL,
    state varchar(120) NOT NULL,
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (length(btrim(locality)) > 0),
    CHECK (length(btrim(city)) > 0),
    CHECK (length(btrim(state)) > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS service_areas_identity_ci_idx
    ON service_areas (lower(btrim(state)), lower(btrim(city)), lower(btrim(locality)));

-- Existing addresses already carry the full geographic tuple. Preserve the
-- first stored spelling while creating one managed identity per tuple.
INSERT INTO service_areas (locality, city, state)
SELECT DISTINCT btrim(locality), btrim(city), btrim(state)
FROM resident_locations
WHERE length(btrim(locality)) > 0 AND length(btrim(city)) > 0 AND length(btrim(state)) > 0
ON CONFLICT DO NOTHING;

ALTER TABLE resident_locations ADD COLUMN IF NOT EXISTS area_id bigint REFERENCES service_areas(id);
ALTER TABLE area_waste_fees ADD COLUMN IF NOT EXISTS area_id bigint REFERENCES service_areas(id);

UPDATE resident_locations l
SET area_id = a.id
FROM service_areas a
WHERE l.area_id IS NULL
  AND lower(btrim(l.state)) = lower(btrim(a.state))
  AND lower(btrim(l.city)) = lower(btrim(a.city))
  AND lower(btrim(l.locality)) = lower(btrim(a.locality));

-- A legacy fee has only a locality name. Assign it only if exactly one
-- managed area currently has that locality; keep ambiguous/unmatched rows.
WITH unambiguous AS (
    SELECT f.id AS fee_id, min(a.id) AS area_id
    FROM area_waste_fees f
    JOIN service_areas a ON lower(btrim(a.locality)) = lower(btrim(f.area_name))
    GROUP BY f.id
    HAVING count(*) = 1
)
UPDATE area_waste_fees f
SET area_id = u.area_id
FROM unambiguous u
WHERE f.id = u.fee_id AND f.area_id IS NULL;

DROP INDEX IF EXISTS area_waste_fees_name_ci_idx;
CREATE INDEX IF NOT EXISTS resident_locations_area_idx ON resident_locations(area_id) WHERE area_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS area_waste_fees_area_id_unique_idx ON area_waste_fees(area_id) WHERE area_id IS NOT NULL;

COMMIT;
