-- Runs automatically on first postgres container start via /docker-entrypoint-initdb.d/
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ─── Organizations ────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS organizations (
    id          UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    name        TEXT        NOT NULL UNIQUE,
    slug        TEXT        NOT NULL UNIQUE,
    description TEXT        NOT NULL DEFAULT '',
    status      TEXT        NOT NULL DEFAULT 'active',
    is_default  BOOLEAN     NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS organizations_default_idx ON organizations(is_default) WHERE is_default = true;

CREATE TABLE IF NOT EXISTS projects (
    id         UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id     UUID        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name       TEXT        NOT NULL,
    status     TEXT        NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);

CREATE TABLE IF NOT EXISTS users (
    id            UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id        UUID        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email         TEXT        NOT NULL UNIQUE,
    password_hash TEXT        NOT NULL,
    role          TEXT        NOT NULL DEFAULT 'member',      -- legacy; kept for JWT compat
    org_role      TEXT        NOT NULL DEFAULT 'member',      -- 'platform_admin' | 'org_admin' | 'member'
    project_id    UUID        REFERENCES projects(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS quotas (
    project_id UUID   REFERENCES projects(id),
    resource   TEXT   NOT NULL,
    hard_limit INT    NOT NULL,
    used       INT    NOT NULL DEFAULT 0,
    PRIMARY KEY (project_id, resource)
);

CREATE TABLE IF NOT EXISTS audit_events (
    id            BIGSERIAL PRIMARY KEY,
    project_id    UUID,
    user_id       UUID,
    resource_type TEXT        NOT NULL,
    resource_id   UUID,
    action        TEXT        NOT NULL,
    old_state     TEXT,
    new_state     TEXT,
    request_id    UUID,
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS audit_events_project_id_idx  ON audit_events(project_id);
CREATE INDEX IF NOT EXISTS audit_events_resource_id_idx ON audit_events(resource_id);
CREATE INDEX IF NOT EXISTS audit_events_occurred_at_idx ON audit_events(occurred_at DESC);

CREATE TABLE IF NOT EXISTS keypairs (
    id          UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    public_key  TEXT        NOT NULL,
    fingerprint TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, name)
);

CREATE INDEX IF NOT EXISTS keypairs_user_id_idx ON keypairs(user_id);

-- Many-to-many project membership.
-- users.project_id remains the "home" (default) project.
CREATE TABLE IF NOT EXISTS project_members (
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    PRIMARY KEY (project_id, user_id)
);

CREATE INDEX IF NOT EXISTS project_members_user_id_idx ON project_members(user_id);

-- ─── Identity Providers (OIDC / SSO) ─────────────────────────────────────────
CREATE TABLE IF NOT EXISTS identity_providers (
    id             UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id         UUID        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name           TEXT        NOT NULL,
    -- slug is a URL-safe, human-readable identifier derived from name.
    -- It is used in OIDC redirect URIs so admins can register a stable, readable
    -- pattern in their IDP (e.g. http://localhost:5173/v1/auth/oidc/acme-sso/callback)
    -- rather than a UUID.
    slug           TEXT        NOT NULL,
    provider_type  TEXT        NOT NULL DEFAULT 'oidc',
    issuer_url     TEXT        NOT NULL,
    client_id      TEXT        NOT NULL,
    client_secret  TEXT        NOT NULL,
    scopes         TEXT        NOT NULL DEFAULT 'openid email profile',
    claim_email    TEXT        NOT NULL DEFAULT 'email',
    claim_name     TEXT        NOT NULL DEFAULT 'name',
    auto_provision BOOLEAN     NOT NULL DEFAULT true,
    default_role   TEXT        NOT NULL DEFAULT 'member',
    domain_hint    TEXT,
    enabled        BOOLEAN     NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, name),
    UNIQUE (org_id, slug)
);

CREATE INDEX IF NOT EXISTS identity_providers_org_id_idx ON identity_providers(org_id);

CREATE TABLE IF NOT EXISTS federated_identities (
    id             UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id        UUID        NOT NULL REFERENCES users(id)              ON DELETE CASCADE,
    idp_id         UUID        NOT NULL REFERENCES identity_providers(id) ON DELETE CASCADE,
    external_sub   TEXT        NOT NULL,
    external_email TEXT,
    last_login_at  TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (idp_id, external_sub)
);

CREATE INDEX IF NOT EXISTS federated_identities_user_id_idx ON federated_identities(user_id);

-- ─── Personal Access Tokens ───────────────────────────────────────────────────
-- Long-lived opaque tokens that users can create for CLI / IaC use.
-- The raw token value is shown once at creation time; only its bcrypt hash is stored.
CREATE TABLE IF NOT EXISTS personal_access_tokens (
    id          UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    token_hash  TEXT        NOT NULL,       -- bcrypt of the raw token
    last_used_at TIMESTAMPTZ,
    expires_at  TIMESTAMPTZ,               -- NULL = never expires
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, name)
);

CREATE INDEX IF NOT EXISTS personal_access_tokens_user_id_idx ON personal_access_tokens(user_id);
