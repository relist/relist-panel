-- +goose Up
CREATE TABLE environments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL COLLATE NOCASE,
    created_at INTEGER NOT NULL,
    UNIQUE (project_id, name)
);

CREATE INDEX environments_project_id_idx ON environments(project_id);

-- +goose Down
DROP TABLE environments;
