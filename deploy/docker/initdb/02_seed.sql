-- Seed the built-in Pulsar operator org, its default project, and the initial
-- platform admin user on first container start.
-- This file only runs once (Docker initdb skips it if the DB already exists).
--
-- Password for admin@pulsar.dev is bcrypt("admin") — change it immediately.
-- Generate a fresh hash with: go run hack/bcrypt_hash.go <password>

-- ─── Default org (Pulsar service provider) ───────────────────────────────────
INSERT INTO organizations (id, name, slug, description, is_default)
VALUES (
  '00000000-0000-0000-0000-000000000010',
  'Pulsar',
  'pulsar',
  'Built-in organization owned by the Pulsar service provider.',
  true
)
ON CONFLICT (slug) DO NOTHING;

-- ─── Default project ─────────────────────────────────────────────────────────
INSERT INTO projects (id, org_id, name, status)
VALUES (
  '00000000-0000-0000-0000-000000000001',
  '00000000-0000-0000-0000-000000000010',
  'default',
  'active'
)
ON CONFLICT (org_id, name) DO NOTHING;

-- ─── Platform admin user ─────────────────────────────────────────────────────
-- bcrypt hash of the literal string "admin" at cost 10.
INSERT INTO users (id, org_id, email, password_hash, role, org_role, project_id)
VALUES (
  '00000000-0000-0000-0000-000000000002',
  '00000000-0000-0000-0000-000000000010',
  'admin@pulsar.dev',
  '$2a$10$OOgnNKcQLKR66KHMmHUBOe8zIPh6/iNkjDbeUtjXeT2NGOV0.Zsva',
  'admin',
  'platform_admin',
  '00000000-0000-0000-0000-000000000001'
)
ON CONFLICT (email) DO NOTHING;

-- ─── Project membership ───────────────────────────────────────────────────────
INSERT INTO project_members (project_id, user_id)
VALUES ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002')
ON CONFLICT DO NOTHING;
