-- +migrate Up

-- ─── Identity provider slugs ──────────────────────────────────────────────────
-- The OIDC routes (/v1/auth/oidc/{slug}/authorize|callback) look IDPs up by
-- slug ALONE, so slugs must be unique across all orgs. They were previously
-- (a) missing entirely from databases built from migrations, which made
-- CreateIDP fail, and (b) only unique per org in initdb-built databases, so
-- two orgs naming an IDP "Google" made SSO resolve to an arbitrary org's IDP.

ALTER TABLE identity_providers ADD COLUMN IF NOT EXISTS slug TEXT;

-- Backfill from the name using the same rules as slugify() in idp.go.
UPDATE identity_providers
   SET slug = trim(both '-' from regexp_replace(lower(trim(name)), '[^a-z0-9]+', '-', 'g'))
 WHERE slug IS NULL OR slug = '';

-- De-duplicate: the oldest IDP keeps the slug, later ones get an id suffix.
UPDATE identity_providers ip
   SET slug = ip.slug || '-' || left(ip.id::text, 8)
 WHERE EXISTS (
       SELECT 1 FROM identity_providers o
        WHERE o.slug = ip.slug AND o.id <> ip.id
          AND (o.created_at, o.id) < (ip.created_at, ip.id));

ALTER TABLE identity_providers ALTER COLUMN slug SET NOT NULL;
ALTER TABLE identity_providers DROP CONSTRAINT IF EXISTS identity_providers_org_id_slug_key;
CREATE UNIQUE INDEX IF NOT EXISTS identity_providers_slug_key ON identity_providers (slug);

-- +migrate Down

DROP INDEX IF EXISTS identity_providers_slug_key;
