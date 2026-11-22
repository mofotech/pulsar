-- +migrate Up

-- ─── Project roles ────────────────────────────────────────────────────────────
-- Each membership now carries a role:
--   reader  read-only access to the project's resources
--   member  manage resources (the previous behaviour for every member)
--   admin   also manage the project's membership
-- Existing memberships become 'member', so nobody loses access.
ALTER TABLE project_members ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'member';
ALTER TABLE project_members DROP CONSTRAINT IF EXISTS project_members_role_check;
ALTER TABLE project_members ADD CONSTRAINT project_members_role_check
    CHECK (role IN ('reader', 'member', 'admin'));

-- +migrate Down

ALTER TABLE project_members DROP CONSTRAINT IF EXISTS project_members_role_check;
ALTER TABLE project_members DROP COLUMN IF EXISTS role;
