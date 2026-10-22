CREATE TABLE IF NOT EXISTS monthly_service_invoices (
    id bigserial PRIMARY KEY,
    reference varchar(40) NOT NULL UNIQUE,
    resident_id bigint NOT NULL REFERENCES users(id),
    location_id bigint NOT NULL REFERENCES resident_locations(id),
    billing_month date NOT NULL,
    due_date date NOT NULL,
    amount_kobo bigint NOT NULL CHECK (amount_kobo > 0),
    fee_breakdown jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'due' CHECK (status IN ('due','paid','void')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (billing_month = date_trunc('month', billing_month)::date),
    UNIQUE (resident_id, location_id, billing_month)
);

CREATE INDEX IF NOT EXISTS monthly_service_invoices_resident_due_idx
    ON monthly_service_invoices(resident_id, due_date DESC, billing_month DESC);

CREATE INDEX IF NOT EXISTS monthly_service_invoices_location_month_idx
    ON monthly_service_invoices(location_id, billing_month DESC);
