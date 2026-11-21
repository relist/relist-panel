-- +goose Up
-- Phase 7 (.scratch/databases/plan-phase-7.md, ADR-0044): the login users of a
-- panel-managed SQL Database whose password the panel stores because it made
-- or reset them, and the superuser password a reset has set live but not yet
-- made current (the pending password survives a crash between those steps).
CREATE TABLE database_users (
    database_id INTEGER NOT NULL REFERENCES databases(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    password TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (database_id, name)
);
ALTER TABLE databases ADD COLUMN pending_password TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE databases DROP COLUMN pending_password;
DROP TABLE database_users;
