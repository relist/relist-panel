-- +goose Up
CREATE TABLE environment_variables (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    environment_id INTEGER NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    UNIQUE (environment_id, key)
);

CREATE INDEX environment_variables_environment_id_idx ON environment_variables(environment_id);

-- +goose Down
DROP TABLE environment_variables;
