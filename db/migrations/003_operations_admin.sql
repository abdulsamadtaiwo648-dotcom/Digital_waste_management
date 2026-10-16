CREATE TABLE collection_jobs (
    id bigserial PRIMARY KEY,
    container_id text NOT NULL REFERENCES containers(id),
    assigned_to bigint REFERENCES users(id),
    scheduled_at timestamptz NOT NULL,
    status text NOT NULL DEFAULT 'scheduled' CHECK (status IN ('scheduled','assigned','in_progress','completed','cancelled','overdue')),
    completion_note text NOT NULL DEFAULT '',
    created_by bigint NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX collection_jobs_schedule_idx ON collection_jobs (scheduled_at,status);

CREATE TABLE inspections (
    id bigserial PRIMARY KEY,
    container_id text NOT NULL REFERENCES containers(id),
    inspector_id bigint NOT NULL REFERENCES users(id),
    condition text NOT NULL CHECK (condition IN ('good','needs_attention','damaged','missing')),
    notes text NOT NULL DEFAULT '',
    inspected_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX inspections_container_date_idx ON inspections (container_id,inspected_at DESC);

CREATE TABLE notifications (
    id bigserial PRIMARY KEY,
    target_role text NOT NULL CHECK (target_role IN ('operations_manager','system_administrator')),
    title varchar(180) NOT NULL,
    message text NOT NULL,
    resource_type text NOT NULL DEFAULT '',
    resource_id text NOT NULL DEFAULT '',
    dedupe_key text UNIQUE,
    read_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notifications_role_created_idx ON notifications (target_role,created_at DESC);

CREATE TABLE announcements (
    id bigserial PRIMARY KEY,
    title varchar(180) NOT NULL,
    body text NOT NULL,
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published','archived')),
    author_id bigint NOT NULL REFERENCES users(id),
    published_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX announcements_public_idx ON announcements (published_at DESC) WHERE status='published';

CREATE TABLE application_settings (
    key text PRIMARY KEY,
    value jsonb NOT NULL,
    updated_by bigint NOT NULL REFERENCES users(id),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_logs (
    id bigserial PRIMARY KEY,
    actor_id bigint REFERENCES users(id),
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL DEFAULT '',
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_logs_created_idx ON audit_logs (created_at DESC);
