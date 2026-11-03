-- +goose Up
-- The directories a Git Source checks out, as a JSON array. Empty means the
-- whole repository.
ALTER TABLE compose_sources ADD COLUMN sparse_paths TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE compose_sources DROP COLUMN sparse_paths;
