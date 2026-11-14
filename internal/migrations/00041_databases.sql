-- +goose Up
-- Panel-managed Databases (.scratch/databases/spec.md, ADR-0039).
-- One row per Database. The container and volume are named from id, never
-- from name. desired is what the Admin wants (running or stopped);
-- initialized is set once the container has started on its volume, so a
-- missing volume afterwards is reported rather than recreated.
CREATE TABLE databases (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE COLLATE NOCASE,
    slug TEXT NOT NULL,
    engine TEXT NOT NULL,
    version TEXT NOT NULL,
    password TEXT NOT NULL,
    memory_mb INTEGER NOT NULL DEFAULT 0,
    cpus REAL NOT NULL DEFAULT 0,
    desired TEXT NOT NULL DEFAULT 'running',
    initialized INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE databases;
