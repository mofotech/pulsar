-- +migrate Up

-- ─── Identity Providers ───────────────────────────────────────────────────────
-- Each org can register one or more OIDC providers.
-- An org_admin configures these so their users can log in via SSO.

CREATE TABLE identity_providers (
    id             UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id         UUID        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name           TEXT        NOT NULL,                    -- display label, e.g. "Google"
    provider_type  TEXT        NOT NULL DEFAULT 'oidc',     -- 'oidc' (saml future)
    issuer_url     TEXT        NOT NULL,                    -- e.g. https://accounts.google.com
    client_id      TEXT        NOT NULL,
    client_secret  TEXT        NOT NULL,                    -- stored as-is; encrypt at rest in Phase 13
    scopes         TEXT        NOT NULL DEFAULT 'openid email profile',
    -- Claim mapping
    claim_email    TEXT        NOT NULL DEFAULT 'email',
    claim_name     TEXT        NOT NULL DEFAULT 'name',
    -- Auto-provision a local user record on first federated login
    auto_provision BOOLEAN     NOT NULL DEFAULT true,
    default_role   TEXT        NOT NULL DEFAULT 'member',   -- role given to auto-provisioned users
    -- Optional: restrict to users whose email matches this domain, e.g. "acme.com"
    domain_hint    TEXT,
    enabled        BOOLEAN     NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);

CREATE INDEX identity_providers_org_id_idx ON identity_providers(org_id);

-- ─── Federated identity links ─────────────────────────────────────────────────
-- Binds an IDP's external "sub" claim to a local Pulsar user record.
-- Created on first successful OIDC login when auto_provision = true.

CREATE TABLE federated_identities (
    id             UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id        UUID        NOT NULL REFERENCES users(id)               ON DELETE CASCADE,
    idp_id         UUID        NOT NULL REFERENCES identity_providers(id)  ON DELETE CASCADE,
    external_sub   TEXT        NOT NULL,   -- "sub" claim from IDP token
    external_email TEXT,
    last_login_at  TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (idp_id, external_sub)
);

CREATE INDEX federated_identities_user_id_idx ON federated_identities(user_id);

-- +migrate Down

DROP TABLE IF EXISTS federated_identities;
DROP TABLE IF EXISTS identity_providers;
