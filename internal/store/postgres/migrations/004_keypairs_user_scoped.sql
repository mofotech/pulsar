-- +migrate Up

-- Add user_id column (nullable initially so we can backfill).
ALTER TABLE keypairs ADD COLUMN user_id UUID REFERENCES users(id) ON DELETE CASCADE;

-- Backfill: assign existing keypairs to a user in the same project.
-- If multiple users share a project, pick the first by created_at.
UPDATE keypairs k
   SET user_id = (
       SELECT u.id FROM users u
        WHERE u.project_id = k.project_id
        ORDER BY u.created_at ASC
        LIMIT 1
   );

-- Remove old project-scoped constraints and column.
ALTER TABLE keypairs DROP CONSTRAINT IF EXISTS keypairs_project_id_name_key;
DROP INDEX IF EXISTS keypairs_project_id_idx;
ALTER TABLE keypairs DROP COLUMN project_id;

-- Make user_id NOT NULL now that it's been populated.
ALTER TABLE keypairs ALTER COLUMN user_id SET NOT NULL;

-- New uniqueness: a user cannot have two keypairs with the same name.
ALTER TABLE keypairs ADD CONSTRAINT keypairs_user_id_name_key UNIQUE (user_id, name);

CREATE INDEX keypairs_user_id_idx ON keypairs(user_id);

-- +migrate Down

ALTER TABLE keypairs DROP CONSTRAINT IF EXISTS keypairs_user_id_name_key;
DROP INDEX IF EXISTS keypairs_user_id_idx;
ALTER TABLE keypairs ADD COLUMN project_id UUID REFERENCES projects(id) ON DELETE CASCADE;
ALTER TABLE keypairs DROP COLUMN IF EXISTS user_id;
