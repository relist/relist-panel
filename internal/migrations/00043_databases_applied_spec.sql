-- +goose Up
-- The container settings a Database was last created with (ADR-0039). Reconcile
-- recreates a container from these, not from what the running panel's driver
-- would choose today, so a Panel update never restarts a Database. New driver
-- defaults wait until the Admin applies them. '' is filled from the current
-- driver on the next reconcile.
ALTER TABLE databases ADD COLUMN applied_spec TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE databases DROP COLUMN applied_spec;
