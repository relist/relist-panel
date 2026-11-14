-- Basic auth groups (ADR-0036). A group is a named list of users that a Domain
-- may require. Passwords are kept in the clear next to their bcrypt hash so an
-- Admin can reveal them again; the hash is what Traefik's file provider gets.

-- +goose Up
CREATE TABLE auth_groups (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE auth_group_users (
    id         INTEGER PRIMARY KEY,
    group_id   INTEGER NOT NULL REFERENCES auth_groups(id) ON DELETE CASCADE,
    username   TEXT    NOT NULL,
    password   TEXT    NOT NULL,
    hash       TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (group_id, username)
);

ALTER TABLE domains ADD COLUMN auth_group_id INTEGER REFERENCES auth_groups(id);

-- +goose Down
ALTER TABLE domains DROP COLUMN auth_group_id;
DROP TABLE auth_group_users;
DROP TABLE auth_groups;
