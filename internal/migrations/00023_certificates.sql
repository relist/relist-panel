-- +goose Up
-- Certificates the operator uploads; the panel writes each into Traefik's
-- file-provider directory, and Traefik serves it by SNI (ADR-0022).
CREATE TABLE certificates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    certificate_pem TEXT NOT NULL,
    private_key_pem TEXT NOT NULL,
    -- Read from the leaf on every write, never edited directly.
    sans TEXT NOT NULL,
    issuer TEXT NOT NULL,
    not_after INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE certificates;
