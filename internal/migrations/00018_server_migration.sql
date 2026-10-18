-- +goose Up
-- One row each: a panel has at most one Migration token, one claim counter and
-- one Migration lock (ADR-0018). Tokens and session keys are stored as SHA-256.
CREATE TABLE migration_token (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    token_hash TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    session_hash TEXT,
    claimed_by TEXT,
    claimed_at INTEGER
);
CREATE TABLE migration_claim_failures (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    failures INTEGER NOT NULL,
    locked_until INTEGER
);
CREATE TABLE migration_lock (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    step TEXT NOT NULL,
    state TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE migration_lock;
DROP TABLE migration_claim_failures;
DROP TABLE migration_token;
