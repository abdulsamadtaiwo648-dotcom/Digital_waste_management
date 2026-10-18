BEGIN;

-- A saved resident address is the parent premise. Its units are independently
-- addressable (for example, upstairs flat, downstairs flat, or Shop 4).
CREATE TABLE IF NOT EXISTS registered_properties (
    id bigserial PRIMARY KEY,
    location_id bigint NOT NULL UNIQUE REFERENCES resident_locations(id),
    area_id bigint NOT NULL REFERENCES service_areas(id),
    address_key text NOT NULL,
    registered_by bigint NOT NULL REFERENCES users(id),
    property_type text NOT NULL CHECK (property_type IN ('residential','commercial','mixed_use','other')),
    structure_type text NOT NULL CHECK (structure_type IN ('bungalow','storey_building','apartment_block','shop_complex','mixed_use','other')),
    floors smallint NOT NULL CHECK (floors BETWEEN 1 AND 100),
    registration_scope text NOT NULL CHECK (registration_scope IN ('whole_property','single_unit')),
    registrant_role text NOT NULL CHECK (registrant_role IN ('owner','tenant','caretaker','agent')),
    expected_bin_count smallint NOT NULL CHECK (expected_bin_count BETWEEN 1 AND 100),
    status text NOT NULL DEFAULT 'awaiting_bin_delivery' CHECK (status IN ('awaiting_bin_delivery','verified_by_delivery','needs_review','inactive')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS registered_properties_area_address_idx
    ON registered_properties(area_id,address_key);

CREATE TABLE IF NOT EXISTS property_units (
    id bigserial PRIMARY KEY,
    property_id bigint NOT NULL REFERENCES registered_properties(id),
    unit_code varchar(80) NOT NULL,
    unit_type text NOT NULL CHECK (unit_type IN ('residential','shop','office','shared','other')),
    floor_label varchar(80) NOT NULL DEFAULT '',
    service_scope text NOT NULL CHECK (service_scope IN ('individual_bin','shared_bin')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(property_id, unit_code),
    UNIQUE(id, property_id)
);

CREATE TABLE IF NOT EXISTS property_account_memberships (
    property_id bigint NOT NULL REFERENCES registered_properties(id),
    unit_id bigint NOT NULL REFERENCES property_units(id),
    user_id bigint NOT NULL REFERENCES users(id),
    relationship text NOT NULL CHECK (relationship IN ('owner','tenant','caretaker','agent')),
    is_primary_contact boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(unit_id,user_id),
    FOREIGN KEY(unit_id,property_id) REFERENCES property_units(id,property_id)
);
CREATE INDEX IF NOT EXISTS property_members_user_idx ON property_account_memberships(user_id,created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS property_one_primary_contact_idx ON property_account_memberships(unit_id) WHERE is_primary_contact;

CREATE TABLE IF NOT EXISTS property_unit_requests (
    id bigserial PRIMARY KEY,
    property_id bigint NOT NULL REFERENCES registered_properties(id),
    location_id bigint NOT NULL REFERENCES resident_locations(id),
    requester_id bigint NOT NULL REFERENCES users(id),
    unit_code varchar(80) NOT NULL,
    unit_type text NOT NULL CHECK (unit_type IN ('residential','shop','office','shared','other')),
    floor_label varchar(80) NOT NULL DEFAULT '',
    service_scope text NOT NULL CHECK (service_scope IN ('individual_bin','shared_bin')),
    relationship text NOT NULL CHECK (relationship IN ('owner','tenant','caretaker','agent')),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    reviewed_by bigint REFERENCES users(id),
    reviewed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((status='pending' AND reviewed_by IS NULL AND reviewed_at IS NULL) OR (status<>'pending' AND reviewed_by IS NOT NULL AND reviewed_at IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS property_unit_request_pending_unique_idx
    ON property_unit_requests(property_id,requester_id,lower(btrim(unit_code))) WHERE status='pending';
CREATE INDEX IF NOT EXISTS property_unit_requests_queue_idx ON property_unit_requests(created_at) WHERE status='pending';

CREATE TABLE IF NOT EXISTS property_bins (
    id bigserial PRIMARY KEY,
    property_id bigint NOT NULL REFERENCES registered_properties(id),
    unit_id bigint REFERENCES property_units(id),
    tracking_id varchar(80) NOT NULL UNIQUE,
    size_litres integer NOT NULL CHECK (size_litres > 0),
    status text NOT NULL DEFAULT 'awaiting_delivery' CHECK (status IN ('awaiting_delivery','delivered','failed','retired')),
    created_by bigint NOT NULL REFERENCES users(id),
    delivered_at timestamptz,
    delivered_by bigint REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((status='delivered' AND delivered_at IS NOT NULL AND delivered_by IS NOT NULL) OR status<>'delivered'),
    FOREIGN KEY(unit_id,property_id) REFERENCES property_units(id,property_id)
);
CREATE INDEX IF NOT EXISTS property_bins_pending_idx ON property_bins(status,created_at) WHERE status='awaiting_delivery';

CREATE TABLE IF NOT EXISTS area_bin_fees (
    id bigserial PRIMARY KEY,
    area_id bigint NOT NULL REFERENCES service_areas(id),
    size_litres integer NOT NULL CHECK (size_litres > 0),
    amount_kobo bigint NOT NULL CHECK (amount_kobo >= 0),
    updated_by bigint NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(area_id,size_litres)
);

CREATE TABLE IF NOT EXISTS bin_delivery_events (
    id bigserial PRIMARY KEY,
    bin_id bigint NOT NULL REFERENCES property_bins(id),
    staff_id bigint NOT NULL REFERENCES users(id),
    outcome text NOT NULL CHECK (outcome IN ('delivered','failed')),
    note varchar(500) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS bin_delivery_events_bin_idx ON bin_delivery_events(bin_id,created_at DESC);

COMMIT;
