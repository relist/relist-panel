-- +goose Up
CREATE TABLE compose_variables (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    compose_id INTEGER NOT NULL REFERENCES composes(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    secret INTEGER NOT NULL DEFAULT 0,
    UNIQUE (compose_id, key)
);

CREATE TABLE variable_sets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    compose_id INTEGER NOT NULL REFERENCES composes(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (compose_id, name)
);

CREATE TABLE variable_set_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    set_id INTEGER NOT NULL REFERENCES variable_sets(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    secret INTEGER NOT NULL DEFAULT 0,
    UNIQUE (set_id, key)
);

CREATE TABLE service_bindings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    compose_id INTEGER NOT NULL REFERENCES composes(id) ON DELETE CASCADE,
    service TEXT NOT NULL,
    set_id INTEGER NOT NULL REFERENCES variable_sets(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    UNIQUE (compose_id, service, set_id)
);

CREATE INDEX compose_variables_compose_id_idx ON compose_variables(compose_id);
CREATE INDEX variable_sets_compose_id_idx ON variable_sets(compose_id);
CREATE INDEX variable_set_entries_set_id_idx ON variable_set_entries(set_id);
CREATE INDEX service_bindings_compose_id_idx ON service_bindings(compose_id);
CREATE INDEX service_bindings_set_id_idx ON service_bindings(set_id);

ALTER TABLE composes ADD COLUMN variables_changed_at INTEGER;

-- +goose Down
ALTER TABLE composes DROP COLUMN variables_changed_at;
DROP TABLE service_bindings;
DROP TABLE variable_set_entries;
DROP TABLE variable_sets;
DROP TABLE compose_variables;
