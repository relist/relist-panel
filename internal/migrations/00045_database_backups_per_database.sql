-- +goose Up
-- Phase 2b (.scratch/databases/plan-phase-2b.md): a Backup is one logical
-- database, and a restore goes into a target the Admin picks. The scope
-- column is still written ('{}') and never read; it is not dropped here.
ALTER TABLE database_backups ADD COLUMN logical_name TEXT NOT NULL DEFAULT '';
ALTER TABLE database_restores ADD COLUMN target TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE database_restores DROP COLUMN target;
ALTER TABLE database_backups DROP COLUMN logical_name;
