-- +migrate Up

-- Many-to-many: a user may belong to multiple projects.
-- users.project_id is kept as the "home" project (used as the default when no
-- X-Project-Id header is present) but is no longer the sole access control gate.
CREATE TABLE project_members (
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    PRIMARY KEY (project_id, user_id)
);

CREATE INDEX project_members_user_id_idx ON project_members(user_id);

-- Backfill: every existing user is a member of their home project.
INSERT INTO project_members (project_id, user_id)
SELECT project_id, id
  FROM users
 WHERE project_id IS NOT NULL
ON CONFLICT DO NOTHING;

-- +migrate Down

DROP TABLE IF EXISTS project_members;
