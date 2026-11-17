-- +goose Up
-- Phase 9 (.scratch/databases/plan-phase-9.md, ADR-0046): GFS Rotation modes
-- per set, and encrypted copies. passphrase is a secret like secret: the API
-- never returns it outside the audited reveal.
ALTER TABLE database_backup_schedules ADD COLUMN local_mode TEXT NOT NULL DEFAULT 'simple';
ALTER TABLE database_backup_schedules ADD COLUMN local_gfs_days INTEGER;
ALTER TABLE database_backup_schedules ADD COLUMN local_gfs_weeks INTEGER;
ALTER TABLE database_backup_schedules ADD COLUMN local_gfs_months INTEGER;
ALTER TABLE database_backup_schedules ADD COLUMN local_gfs_years INTEGER;
ALTER TABLE database_backup_schedules ADD COLUMN remote_mode TEXT NOT NULL DEFAULT 'simple';
ALTER TABLE database_backup_schedules ADD COLUMN remote_gfs_days INTEGER;
ALTER TABLE database_backup_schedules ADD COLUMN remote_gfs_weeks INTEGER;
ALTER TABLE database_backup_schedules ADD COLUMN remote_gfs_months INTEGER;
ALTER TABLE database_backup_schedules ADD COLUMN remote_gfs_years INTEGER;
ALTER TABLE backup_destinations ADD COLUMN encrypt INTEGER NOT NULL DEFAULT 0;
ALTER TABLE backup_destinations ADD COLUMN passphrase TEXT NOT NULL DEFAULT '';
ALTER TABLE database_backup_uploads ADD COLUMN encrypted INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE database_backup_uploads DROP COLUMN encrypted;
ALTER TABLE backup_destinations DROP COLUMN passphrase;
ALTER TABLE backup_destinations DROP COLUMN encrypt;
ALTER TABLE database_backup_schedules DROP COLUMN remote_gfs_years;
ALTER TABLE database_backup_schedules DROP COLUMN remote_gfs_months;
ALTER TABLE database_backup_schedules DROP COLUMN remote_gfs_weeks;
ALTER TABLE database_backup_schedules DROP COLUMN remote_gfs_days;
ALTER TABLE database_backup_schedules DROP COLUMN remote_mode;
ALTER TABLE database_backup_schedules DROP COLUMN local_gfs_years;
ALTER TABLE database_backup_schedules DROP COLUMN local_gfs_months;
ALTER TABLE database_backup_schedules DROP COLUMN local_gfs_weeks;
ALTER TABLE database_backup_schedules DROP COLUMN local_gfs_days;
ALTER TABLE database_backup_schedules DROP COLUMN local_mode;
