CREATE TABLE containers (
    id text PRIMARY KEY,
    label text NOT NULL,
    service_area text NOT NULL,
    usable_depth_cm numeric(8,2) NOT NULL CHECK (usable_depth_cm > 0),
    minimum_distance_cm numeric(8,2) NOT NULL DEFAULT 2 CHECK (minimum_distance_cm >= 0 AND minimum_distance_cm < usable_depth_cm),
    installation_offset_cm numeric(8,2) NOT NULL DEFAULT 0 CHECK (installation_offset_cm >= 0),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE devices (
    id text PRIMARY KEY,
    container_id text NOT NULL REFERENCES containers(id),
    token_hash text NOT NULL,
    is_simulated boolean NOT NULL DEFAULT false,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sensor_readings (
    id bigserial PRIMARY KEY,
    device_id text NOT NULL REFERENCES devices(id),
    measured_at timestamptz NOT NULL,
    distance_cm numeric(8,2) NOT NULL CHECK (distance_cm >= 0),
    fill_percent numeric(5,2) NOT NULL CHECK (fill_percent BETWEEN 0 AND 100),
    level text NOT NULL CHECK (level IN ('normal','warning','critical')),
    is_simulated boolean NOT NULL DEFAULT false,
    received_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (device_id, measured_at)
);

CREATE INDEX sensor_readings_device_measured_idx ON sensor_readings (device_id, measured_at DESC);
