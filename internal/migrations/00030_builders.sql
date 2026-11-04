-- +goose Up
-- Builders: named, resource-limited buildx builders the panel builds Images on
-- (.scratch/build-resource-limits/spec.md). builder_settings is one row: the
-- panel default Builder (NULL = Docker default) and the BuildKit image ('' =
-- the release's pinned default). compose_builders holds a Compose's choice:
-- no row is the panel default, a NULL builder_id is Docker default.
CREATE TABLE builders (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE COLLATE NOCASE,
    cpus_milli INTEGER NOT NULL,
    memory_mb INTEGER NOT NULL,
    max_parallelism INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE builder_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    default_builder_id INTEGER REFERENCES builders(id) ON DELETE RESTRICT,
    buildkit_image TEXT NOT NULL DEFAULT ''
);
INSERT INTO builder_settings (id) VALUES (1);

CREATE TABLE compose_builders (
    compose_id INTEGER PRIMARY KEY REFERENCES composes(id) ON DELETE CASCADE,
    builder_id INTEGER REFERENCES builders(id) ON DELETE RESTRICT
);

-- +goose Down
DROP TABLE compose_builders;
DROP TABLE builder_settings;
DROP TABLE builders;
