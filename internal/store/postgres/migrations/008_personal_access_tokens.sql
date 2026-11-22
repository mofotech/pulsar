-- +migrate Up

-- ─── Personal Access Tokens ───────────────────────────────────────────────────
-- Previously created only by the Docker/Helm initdb scripts, so databases
-- built from migrations alone (systemd, external Postgres) lacked the table
-- and every PAT endpoint failed. IF NOT EXISTS keeps this a no-op on
-- databases that were bootstrapped by initdb. Keep in sync with
-- deploy/docker/initdb/01_schema.sql.
CREATE TABLE IF NOT EXISTS personal_access_tokens (
    id           UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    token_hash   TEXT        NOT NULL,       -- bcrypt of the raw token
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,                -- NULL = never expires
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, name)
);

CREATE INDEX IF NOT EXISTS personal_access_tokens_user_id_idx ON personal_access_tokens(user_id);

-- +migrate Down

DROP TABLE IF EXISTS personal_access_tokens;
