-- +goose Up
-- The Audit log (.scratch/multi-user/spec.md, ADR-0031): one row per change,
-- refusal, sensitive read or sign-in. The ids it names carry no foreign
-- keys, so an event outlives the User, Project, Environment or Compose it
-- names; target keeps their readable names from the time.
CREATE TABLE audit_events (
    id INTEGER PRIMARY KEY,
    at INTEGER NOT NULL,
    actor_user_id INTEGER NULL,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('ok', 'denied')),
    project_id INTEGER NULL,
    environment_id INTEGER NULL,
    compose_id INTEGER NULL,
    target TEXT NOT NULL,
    detail TEXT NULL,
    ip TEXT NULL
);
CREATE INDEX audit_events_at_idx ON audit_events (at);
CREATE INDEX audit_events_project_idx ON audit_events (project_id, at);
CREATE INDEX audit_events_actor_idx ON audit_events (actor_user_id, at);

-- Retention in days; 0 keeps events forever.
CREATE TABLE audit_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    retention_days INTEGER NOT NULL DEFAULT 90 CHECK (retention_days >= 0)
);
INSERT INTO audit_settings (id, retention_days) VALUES (1, 90);

-- +goose Down
DROP TABLE audit_settings;
DROP TABLE audit_events;
