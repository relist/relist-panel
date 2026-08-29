-- +goose Up
CREATE TABLE admin_credentials (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    email TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE sessions (
    token TEXT PRIMARY KEY,
    admin_id INTEGER NOT NULL REFERENCES admin_credentials(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- +goose Down
DROP TABLE sessions;
DROP TABLE admin_credentials;
