-- +migrate Up

CREATE TABLE keypairs (
    id          UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id  UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    public_key  TEXT        NOT NULL,
    fingerprint TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);

CREATE INDEX keypairs_project_id_idx ON keypairs(project_id);

-- +migrate Down
DROP TABLE IF EXISTS keypairs;
