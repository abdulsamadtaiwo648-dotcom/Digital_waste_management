BEGIN;

-- Keep the existing unique physical tracking_id and unit relationship, while
-- recording which resident account is responsible for this individual bin.
ALTER TABLE property_bins
    ADD COLUMN IF NOT EXISTS assigned_resident_id bigint REFERENCES users(id);

CREATE INDEX IF NOT EXISTS property_bins_assigned_resident_idx
    ON property_bins(assigned_resident_id,property_id)
    WHERE assigned_resident_id IS NOT NULL;

-- Backfill only when existing membership data identifies exactly one account.
-- Ambiguous and missing relationships stay NULL for staff reconciliation.
WITH tenant_contacts AS (
    SELECT b.id AS bin_id,count(DISTINCT m.user_id) AS candidate_count,min(m.user_id) AS user_id
    FROM property_bins b
    JOIN property_account_memberships m ON m.property_id=b.property_id AND m.unit_id=b.unit_id
    JOIN users u ON u.id=m.user_id AND u.role='citizen' AND u.deleted_at IS NULL
    WHERE b.unit_id IS NOT NULL AND m.ended_at IS NULL AND m.relationship='tenant'
    GROUP BY b.id
), unit_contacts AS (
    SELECT b.id AS bin_id,count(DISTINCT m.user_id) AS candidate_count,min(m.user_id) AS user_id
    FROM property_bins b
    JOIN property_account_memberships m ON m.property_id=b.property_id AND m.unit_id=b.unit_id
    JOIN users u ON u.id=m.user_id AND u.role='citizen' AND u.deleted_at IS NULL
    WHERE b.unit_id IS NOT NULL AND m.ended_at IS NULL AND m.is_primary_contact
    GROUP BY b.id
), property_contacts AS (
    SELECT b.id AS bin_id,count(DISTINCT m.user_id) AS candidate_count,min(m.user_id) AS user_id
    FROM property_bins b
    JOIN property_account_memberships m ON m.property_id=b.property_id
    JOIN users u ON u.id=m.user_id AND u.role='citizen' AND u.deleted_at IS NULL
    WHERE b.unit_id IS NULL AND m.ended_at IS NULL AND m.is_primary_contact
    GROUP BY b.id
)
UPDATE property_bins b
SET assigned_resident_id = CASE
    WHEN b.unit_id IS NULL AND pc.candidate_count=1 THEN pc.user_id
    WHEN b.unit_id IS NOT NULL AND tc.candidate_count=1 THEN tc.user_id
    WHEN b.unit_id IS NOT NULL AND COALESCE(tc.candidate_count,0)=0 AND uc.candidate_count=1 THEN uc.user_id
    ELSE NULL
END
FROM tenant_contacts tc
FULL JOIN unit_contacts uc ON uc.bin_id=tc.bin_id
FULL JOIN property_contacts pc ON pc.bin_id=COALESCE(tc.bin_id,uc.bin_id)
WHERE b.id=COALESCE(tc.bin_id,uc.bin_id,pc.bin_id)
  AND b.assigned_resident_id IS NULL;

CREATE TABLE IF NOT EXISTS bin_evacuation_events (
    id bigserial PRIMARY KEY,
    bin_id bigint NOT NULL REFERENCES property_bins(id),
    staff_id bigint NOT NULL REFERENCES users(id),
    evacuated_at timestamptz NOT NULL DEFAULT now(),
    note varchar(500) NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS bin_evacuation_events_bin_time_idx
    ON bin_evacuation_events(bin_id,evacuated_at DESC,id DESC);

COMMIT;
