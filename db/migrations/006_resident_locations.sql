CREATE TABLE IF NOT EXISTS resident_locations (
    id bigserial PRIMARY KEY,
    resident_id bigint NOT NULL REFERENCES users(id),
    label varchar(80) NOT NULL,
    address_line1 varchar(180) NOT NULL,
    address_line2 varchar(180) NOT NULL DEFAULT '',
    locality varchar(120) NOT NULL,
    city varchar(120) NOT NULL,
    state varchar(120) NOT NULL,
    postal_code varchar(24) NOT NULL DEFAULT '',
    latitude double precision,
    longitude double precision,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CHECK ((latitude IS NULL AND longitude IS NULL) OR (latitude BETWEEN -90 AND 90 AND longitude BETWEEN -180 AND 180))
);

CREATE INDEX IF NOT EXISTS resident_locations_owner_idx ON resident_locations(resident_id, created_at) WHERE deleted_at IS NULL;

ALTER TABLE complaints ADD COLUMN IF NOT EXISTS location_id bigint REFERENCES resident_locations(id);
ALTER TABLE payments ADD COLUMN IF NOT EXISTS location_id bigint REFERENCES resident_locations(id);
