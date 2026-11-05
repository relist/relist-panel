-- +goose Up
-- Grants: a Role given to one User on one Project (environment_id NULL), or
-- on one Environment, which may also be No access ('none'). The Environment
-- Grant wins over the Project Grant (.scratch/multi-user/spec.md, ADR-0031).
CREATE TABLE grants (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_id INTEGER NULL REFERENCES environments(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('none', 'viewer', 'operator', 'editor')),
    CHECK (environment_id IS NOT NULL OR role <> 'none')
);
CREATE UNIQUE INDEX grants_project_idx ON grants(user_id, project_id) WHERE environment_id IS NULL;
CREATE UNIQUE INDEX grants_environment_idx ON grants(user_id, environment_id) WHERE environment_id IS NOT NULL;
CREATE INDEX grants_project_id_idx ON grants(project_id);

-- +goose Down
DROP TABLE grants;
