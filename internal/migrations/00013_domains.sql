-- +goose Up
CREATE TABLE domains (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    compose_id INTEGER NOT NULL REFERENCES composes(id) ON DELETE CASCADE,
    host TEXT NOT NULL,
    path TEXT NOT NULL DEFAULT '/',
    internal_path TEXT NOT NULL DEFAULT '/',
    strip_path INTEGER NOT NULL DEFAULT 0 CHECK (strip_path IN (0, 1)),
    service TEXT NOT NULL,
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    entrypoint TEXT NOT NULL DEFAULT '',
    https INTEGER NOT NULL DEFAULT 0 CHECK (https IN (0, 1)),
    cert_provider TEXT NOT NULL DEFAULT 'none' CHECK (cert_provider IN ('none', 'letsencrypt', 'custom')),
    cert_resolver TEXT NOT NULL DEFAULT '',
    middlewares TEXT NOT NULL DEFAULT '[]',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (host, path)
);

CREATE INDEX domains_compose_id_idx ON domains(compose_id);

ALTER TABLE composes ADD COLUMN domains_changed_at INTEGER;

-- +goose Down
ALTER TABLE composes DROP COLUMN domains_changed_at;
DROP TABLE domains;
