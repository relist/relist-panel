-- +goose Up
-- An approver's consent to deploy one resolved config of one Compose
-- (ADR-0032): one per Compose, a new approval replaces it.
CREATE TABLE compose_approvals (
    compose_id INTEGER PRIMARY KEY REFERENCES composes(id) ON DELETE CASCADE,
    config_hash TEXT NOT NULL,
    approved_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    approved_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE compose_approvals;
