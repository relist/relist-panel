-- +goose Up
-- Databases are Admin-only (ADR-0049): non-Admins no longer get access to a
-- Database, so the per-Database grants of 00051 go.
DROP TABLE database_grants;

-- +goose Down
CREATE TABLE database_grants (
    database_id INTEGER NOT NULL REFERENCES databases(id) ON DELETE CASCADE,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    db_user     TEXT NULL,
    level       TEXT NULL CHECK (level IN ('read', 'read_write')),
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (database_id, user_id),
    FOREIGN KEY (database_id, db_user) REFERENCES database_users(database_id, name) ON DELETE CASCADE,
    CHECK ((db_user IS NULL) <> (level IS NULL))
);
CREATE INDEX database_grants_user ON database_grants(user_id);
