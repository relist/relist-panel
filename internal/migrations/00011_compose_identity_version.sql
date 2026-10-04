-- +goose Up
-- Existing Composes retain their name-derived identity and deployed paths.
-- New Composes opt into ID-based identity when created by the repository.
ALTER TABLE composes ADD COLUMN identity_version INTEGER NOT NULL DEFAULT 0 CHECK (identity_version IN (0, 1));

-- +goose Down
ALTER TABLE composes DROP COLUMN identity_version;
