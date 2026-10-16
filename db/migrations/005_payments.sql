CREATE TABLE payments (
    id bigserial PRIMARY KEY,
    reference varchar(30) NOT NULL UNIQUE,
    citizen_id bigint NOT NULL REFERENCES users(id),
    service_code text NOT NULL CHECK (service_code='waste_fee'),
    amount_kobo bigint NOT NULL CHECK (amount_kobo > 0),
    status text NOT NULL CHECK (status IN ('pending','succeeded','failed','cancelled')),
    mode text NOT NULL CHECK (mode IN ('simulation','provider')),
    idempotency_key varchar(100) NOT NULL,
    provider_reference text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (citizen_id,idempotency_key)
);
CREATE INDEX payments_citizen_created_idx ON payments (citizen_id,created_at DESC);

CREATE TABLE payment_status_history (
    id bigserial PRIMARY KEY,
    payment_reference varchar(30) NOT NULL REFERENCES payments(reference),
    status text NOT NULL CHECK (status IN ('pending','succeeded','failed','cancelled')),
    note text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX payment_history_ref_created_idx ON payment_status_history(payment_reference,created_at);

CREATE TABLE receipts (
    reference varchar(32) PRIMARY KEY,
    payment_reference varchar(30) NOT NULL UNIQUE REFERENCES payments(reference),
    issued_at timestamptz NOT NULL DEFAULT now()
);
