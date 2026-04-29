-- +migrate Up

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE projects (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name       TEXT NOT NULL UNIQUE,
    status     TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'member', -- 'admin' | 'member'
    project_id    UUID REFERENCES projects(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE quotas (
    project_id UUID   REFERENCES projects(id),
    resource   TEXT   NOT NULL, -- 'instances' | 'vcpus' | 'ram_mb' | 'volumes' | 'storage_gb'
    hard_limit INT    NOT NULL,
    used       INT    NOT NULL DEFAULT 0,
    PRIMARY KEY (project_id, resource)
);

CREATE TABLE audit_events (
    id            BIGSERIAL PRIMARY KEY,
    project_id    UUID,
    user_id       UUID,
    resource_type TEXT        NOT NULL,
    resource_id   UUID,
    action        TEXT        NOT NULL, -- 'created' | 'deleted' | 'state_changed' | 'updated'
    old_state     TEXT,
    new_state     TEXT,
    request_id    UUID,
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX audit_events_project_id_idx   ON audit_events(project_id);
CREATE INDEX audit_events_resource_id_idx  ON audit_events(resource_id);
CREATE INDEX audit_events_occurred_at_idx  ON audit_events(occurred_at DESC);

-- +migrate Down
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS quotas;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS projects;
