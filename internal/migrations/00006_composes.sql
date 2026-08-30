-- +goose Up
CREATE TABLE composes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    environment_id INTEGER NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name TEXT NOT NULL COLLATE NOCASE,
    yaml_content TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (environment_id, name)
);

CREATE INDEX composes_environment_id_idx ON composes(environment_id);

-- +goose Down
DROP TABLE composes;
