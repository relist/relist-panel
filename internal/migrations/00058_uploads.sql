-- Chunked uploads (.scratch/chunked-upload/spec.md, ADR-0053). One row per
-- upload a User has started: the file's declared size and how many bytes of
-- it are stored and fsynced in <data>/.uploads/<id>. OFFSET is an SQL
-- keyword, so the stored count is `received`.

-- +goose Up
CREATE TABLE uploads (
    id            TEXT    PRIMARY KEY,
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose       TEXT    NOT NULL,
    compose_id    INTEGER REFERENCES composes(id) ON DELETE CASCADE,
    target        TEXT    NOT NULL,
    name          TEXT    NOT NULL,
    size          INTEGER NOT NULL,
    last_modified INTEGER NOT NULL,
    received      INTEGER NOT NULL DEFAULT 0,
    state         TEXT    NOT NULL,
    error_kind    TEXT    NOT NULL DEFAULT '',
    error         TEXT    NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX uploads_user_compose ON uploads(user_id, compose_id, state);

-- +goose Down
DROP TABLE uploads;
