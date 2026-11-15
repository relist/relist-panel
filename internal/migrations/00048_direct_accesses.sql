-- +goose Up
-- Phase 5 (.scratch/databases/spec-2.md "Phase 5: Direct access", ADR-0041):
-- a timed way into one container's port that skips Traefik. The model is
-- generic: a target (kind, id, port), a channel and its JSON config, and an
-- optional expiry. There is no foreign key because target kinds live in
-- different tables, and no unique constraint on the target: one access per
-- Database is a service rule. Times are unix seconds, as 00047.
CREATE TABLE direct_accesses (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    target_kind TEXT NOT NULL,
    target_id INTEGER NOT NULL,
    target_port INTEGER NOT NULL,
    channel TEXT NOT NULL,
    channel_config TEXT NOT NULL,
    expires_at INTEGER,
    opened_by INTEGER,
    opened_at INTEGER NOT NULL
);
CREATE INDEX direct_accesses_target ON direct_accesses (target_kind, target_id);

-- +goose Down
DROP TABLE direct_accesses;
