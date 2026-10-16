CREATE TABLE users (
    id bigserial PRIMARY KEY,
    full_name varchar(120) NOT NULL,
    email varchar(254) NOT NULL UNIQUE,
    phone varchar(32) NOT NULL,
    terms_accepted_at timestamptz NOT NULL DEFAULT now(),
    password_hash text NOT NULL,
    role text NOT NULL DEFAULT 'citizen' CHECK (role IN ('citizen','operations_manager','system_administrator')),
    session_version bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE INDEX users_active_email_idx ON users (lower(email)) WHERE deleted_at IS NULL;

CREATE TABLE complaints (
    reference varchar(20) PRIMARY KEY,
    citizen_id bigint NOT NULL REFERENCES users(id),
    category text NOT NULL CHECK (category IN ('overflowing_skip','missed_collection','illegal_dumping','other')),
    description text NOT NULL CHECK (length(description) BETWEEN 10 AND 4000),
    location varchar(300) NOT NULL,
    status text NOT NULL CHECK (status IN ('submitted','under_review','assigned','in_progress','resolved','rejected')),
    assigned_to bigint REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE INDEX complaints_citizen_created_idx ON complaints (citizen_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX complaints_status_created_idx ON complaints (status, created_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE complaint_status_history (
    id bigserial PRIMARY KEY,
    complaint_reference varchar(20) NOT NULL REFERENCES complaints(reference),
    status text NOT NULL CHECK (status IN ('submitted','under_review','assigned','in_progress','resolved','rejected')),
    changed_by bigint REFERENCES users(id),
    public_message text NOT NULL DEFAULT '',
    internal_note text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX complaint_history_reference_created_idx ON complaint_status_history (complaint_reference, created_at);
