CREATE TABLE IF NOT EXISTS area_waste_fees (
    id bigserial PRIMARY KEY,
    area_name varchar(120) NOT NULL,
    amount_kobo bigint NOT NULL CHECK (amount_kobo >= 0),
    updated_by bigint REFERENCES users(id),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS area_waste_fees_name_ci_idx ON area_waste_fees (lower(btrim(area_name)));
