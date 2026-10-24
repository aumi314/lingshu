CREATE TABLE registration_codes (
    id           BIGSERIAL PRIMARY KEY,
    code_hash    TEXT NOT NULL UNIQUE,
    code_prefix  TEXT NOT NULL,
    created_by   BIGINT NOT NULL REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    used_by      BIGINT REFERENCES users(id),
    used_at      TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);

CREATE INDEX registration_codes_created_at_idx ON registration_codes (created_at DESC);
