-- +goose Up
ALTER TABLE composes ADD COLUMN project_slug TEXT NOT NULL DEFAULT '';
ALTER TABLE composes ADD COLUMN env_slug TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE composes DROP COLUMN project_slug;
ALTER TABLE composes DROP COLUMN env_slug;
