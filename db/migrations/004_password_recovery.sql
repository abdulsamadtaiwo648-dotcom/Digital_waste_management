CREATE TABLE password_reset_tokens (
    id bigserial PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id),
    token_hash char(64) NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX password_reset_user_created_idx ON password_reset_tokens (user_id,created_at DESC);
