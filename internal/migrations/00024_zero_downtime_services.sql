-- +goose Up
-- Services of a Compose whose deploys roll out instead of recreating (ADR-0023).
-- A row means the switch is on; turning it off deletes the row.
CREATE TABLE zero_downtime_services (
    compose_id INTEGER NOT NULL REFERENCES composes(id) ON DELETE CASCADE,
    service TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (compose_id, service)
);

-- +goose Down
DROP TABLE zero_downtime_services;
