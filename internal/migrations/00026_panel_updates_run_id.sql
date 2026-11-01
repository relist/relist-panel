-- +goose Up
-- The run id names the update run's directory under .panel-update. A run's row
-- is written once, when its status.json is terminal, so the id makes that
-- write safe to repeat.
ALTER TABLE panel_updates ADD COLUMN run_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX panel_updates_run_id ON panel_updates (run_id) WHERE run_id <> '';

-- +goose Down
DROP INDEX panel_updates_run_id;
ALTER TABLE panel_updates DROP COLUMN run_id;
