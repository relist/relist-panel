-- Domains can be turned off (.scratch/domain-toggle/spec.md, ADR-0052). A
-- Domain that is off keeps its fields and its host; deploys write no Traefik
-- labels for it. Every existing Domain stays on.

-- +goose Up
ALTER TABLE domains ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE domains DROP COLUMN enabled;
