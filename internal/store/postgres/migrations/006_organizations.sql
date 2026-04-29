-- +migrate Up

-- ─── Organizations ────────────────────────────────────────────────────────────
-- An organization is the top-level tenancy boundary.
-- All projects and users belong to an org.
-- The built-in "pulsar" org (id=00000000-0000-0000-0000-000000000010) is owned
-- by the Pulsar service provider and is used to manage other orgs.

CREATE TABLE organizations (
    id          UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    name        TEXT        NOT NULL UNIQUE,
    slug        TEXT        NOT NULL UNIQUE,  -- URL-safe identifier, e.g. "acme-corp"
    description TEXT        NOT NULL DEFAULT '',
    status      TEXT        NOT NULL DEFAULT 'active', -- 'active' | 'suspended' | 'deleted'
    -- True for the built-in org owned by the platform operator.
    is_default  BOOLEAN     NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Only one default org is allowed.
CREATE UNIQUE INDEX organizations_default_idx ON organizations(is_default) WHERE is_default = true;

-- ─── Scope projects and users to an org ──────────────────────────────────────
ALTER TABLE projects ADD COLUMN org_id UUID REFERENCES organizations(id) ON DELETE CASCADE;
ALTER TABLE users    ADD COLUMN org_id UUID REFERENCES organizations(id) ON DELETE CASCADE;

-- ─── Default org + backfill ───────────────────────────────────────────────────
-- Insert the built-in Pulsar operator org.
INSERT INTO organizations (id, name, slug, description, is_default)
VALUES (
    '00000000-0000-0000-0000-000000000010',
    'Pulsar',
    'pulsar',
    'Built-in organization owned by the Pulsar service provider.',
    true
);

-- All existing projects and users belong to the default org.
UPDATE projects SET org_id = '00000000-0000-0000-0000-000000000010' WHERE org_id IS NULL;
UPDATE users    SET org_id = '00000000-0000-0000-0000-000000000010' WHERE org_id IS NULL;

-- Make the column NOT NULL now that all rows are backfilled.
ALTER TABLE projects ALTER COLUMN org_id SET NOT NULL;
ALTER TABLE users    ALTER COLUMN org_id SET NOT NULL;

-- Project names are unique within an org (not globally).
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_name_key;
ALTER TABLE projects ADD CONSTRAINT projects_name_org_unique UNIQUE (org_id, name);

-- ─── Org-level roles ──────────────────────────────────────────────────────────
-- Extend the role column to support 'org_admin' (manages one org) and
-- 'platform_admin' (manages the platform / all orgs).
-- The legacy 'admin' value is kept valid and treated as 'org_admin' in code
-- until a future migration renames it.
ALTER TABLE users ADD COLUMN org_role TEXT NOT NULL DEFAULT 'member';

-- Promote all current 'admin' users to 'platform_admin' inside the default org
-- (they were the platform operators before orgs existed).
UPDATE users SET org_role = 'platform_admin' WHERE role = 'admin';
UPDATE users SET org_role = 'member'          WHERE role = 'member';

-- +migrate Down

ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_name_org_unique;
ALTER TABLE projects ADD  CONSTRAINT projects_name_key UNIQUE (name);
ALTER TABLE projects DROP COLUMN IF EXISTS org_id;
ALTER TABLE users    DROP COLUMN IF EXISTS org_id;
ALTER TABLE users    DROP COLUMN IF EXISTS org_role;
DROP TABLE IF EXISTS organizations;
