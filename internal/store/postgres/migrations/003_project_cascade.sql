-- +migrate Up

-- Add ON DELETE CASCADE to users.project_id and quotas.project_id so that
-- deleting a project automatically removes its users and quota rows.
ALTER TABLE users
    DROP CONSTRAINT users_project_id_fkey,
    ADD CONSTRAINT users_project_id_fkey
        FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE;

ALTER TABLE quotas
    DROP CONSTRAINT quotas_project_id_fkey,
    ADD CONSTRAINT quotas_project_id_fkey
        FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE;

-- +migrate Down

ALTER TABLE users
    DROP CONSTRAINT users_project_id_fkey,
    ADD CONSTRAINT users_project_id_fkey
        FOREIGN KEY (project_id) REFERENCES projects(id);

ALTER TABLE quotas
    DROP CONSTRAINT quotas_project_id_fkey,
    ADD CONSTRAINT quotas_project_id_fkey
        FOREIGN KEY (project_id) REFERENCES projects(id);
