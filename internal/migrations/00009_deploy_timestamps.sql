-- +goose Up
ALTER TABLE composes ADD COLUMN deployed_at INTEGER;

-- +goose Down
ALTER TABLE composes DROP COLUMN deployed_at;
