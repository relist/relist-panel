-- +goose Up
-- The image the panel ran from before the update, by image ID. Prune plans
-- own the previous panel image through it, so a prune never removes what a
-- manual rollback would need.
ALTER TABLE panel_updates ADD COLUMN from_image TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE panel_updates DROP COLUMN from_image;
