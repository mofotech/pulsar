-- +migrate Up

-- ─── Audit log ────────────────────────────────────────────────────────────────
-- audit_events existed since 001 but nothing wrote to it, and its UUID-typed
-- resource_id/request_id couldn't hold real values (request IDs look like
-- "host/abc-000042"; some resource IDs are slugs). Make them TEXT and add
-- what the recorder captures. Request bodies are never stored.
ALTER TABLE audit_events ALTER COLUMN resource_id TYPE TEXT USING resource_id::text;
ALTER TABLE audit_events ALTER COLUMN request_id  TYPE TEXT USING request_id::text;
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS org_id      UUID;
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS actor_email TEXT;
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS outcome     TEXT;   -- success | failure | denied
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS method      TEXT;
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS route       TEXT;   -- chi pattern, e.g. /v1/compute/instances/{id}
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS path        TEXT;
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS status      INT;
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS source_ip   TEXT;
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS duration_ms INT;

CREATE INDEX IF NOT EXISTS audit_events_org_time_idx  ON audit_events (org_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS audit_events_user_time_idx ON audit_events (user_id, occurred_at DESC);

-- +migrate Down

DROP INDEX IF EXISTS audit_events_user_time_idx;
DROP INDEX IF EXISTS audit_events_org_time_idx;
