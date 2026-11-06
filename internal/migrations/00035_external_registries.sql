-- +goose Up
-- External registries: credentials for registries the panel does not run,
-- written into a per-call Docker config for pulls and builds
-- (.scratch/private-registry/spec.md, ADR-0034). The secret is never returned
-- by the API.
CREATE TABLE external_registries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    host TEXT NOT NULL UNIQUE,
    username TEXT NOT NULL,
    secret TEXT NOT NULL,
    last_test_at INTEGER,
    last_test_ok INTEGER NOT NULL DEFAULT 0,
    last_test_error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE external_registries;
