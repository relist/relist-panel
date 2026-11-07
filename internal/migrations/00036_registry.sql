-- +goose Up
-- The Panel registry (.scratch/private-registry/spec.md, ADR-0033).
-- registry_settings is one row: on/off, the public hostname (NULL until set),
-- how Traefik serves TLS for it, the hidden system login the panel uses
-- (secret and its bcrypt hash) and the last error. registry_credentials are
-- Push credentials: only the bcrypt hash of each token is kept.
CREATE TABLE registry_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    enabled INTEGER NOT NULL DEFAULT 0,
    hostname TEXT UNIQUE,
    tls TEXT NOT NULL DEFAULT 'letsencrypt' CHECK (tls IN ('letsencrypt', 'certificate')),
    system_secret TEXT NOT NULL DEFAULT '',
    system_hash TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT ''
);
INSERT INTO registry_settings (id) VALUES (1);

CREATE TABLE registry_credentials (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    hash TEXT NOT NULL,
    created_by TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE registry_credentials;
DROP TABLE registry_settings;
