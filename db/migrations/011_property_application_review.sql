BEGIN;

-- Keep application review separate from the existing bin/service lifecycle.
-- Legacy properties that were awaiting delivery remain eligible for the
-- established handover flow; already delivered properties are recognized as
-- verified without changing their existing status or history.
ALTER TABLE registered_properties
    ADD COLUMN IF NOT EXISTS review_status text NOT NULL DEFAULT 'awaiting_bin_delivery',
    ADD COLUMN IF NOT EXISTS review_reason varchar(500),
    ADD COLUMN IF NOT EXISTS reviewed_by bigint REFERENCES users(id),
    ADD COLUMN IF NOT EXISTS reviewed_at timestamptz;

UPDATE registered_properties
SET review_status = 'verified'
WHERE status = 'verified_by_delivery'
  AND review_status = 'awaiting_bin_delivery';

ALTER TABLE registered_properties
    DROP CONSTRAINT IF EXISTS registered_properties_review_status_check;
ALTER TABLE registered_properties
    ADD CONSTRAINT registered_properties_review_status_check
    CHECK (review_status IN ('pending_review','awaiting_bin_delivery','verified','rejected'));

CREATE INDEX IF NOT EXISTS registered_properties_review_queue_idx
    ON registered_properties(review_status, created_at DESC)
    WHERE status <> 'inactive';

COMMIT;
