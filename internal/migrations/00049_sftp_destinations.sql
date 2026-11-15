-- +goose Up
-- Phase 6a (.scratch/databases/plan-phase-6a.md, ADR-0042): SFTP Backup
-- destinations. ssh_key_id is the library key an SFTP destination logs in
-- with; RESTRICT keeps a used key from being deleted under it. host_key is
-- the server's trusted host key (one authorized_keys line), kept out of
-- config so recording it never looks like a settings change.
ALTER TABLE backup_destinations ADD COLUMN ssh_key_id INTEGER REFERENCES ssh_keys(id) ON DELETE RESTRICT;
ALTER TABLE backup_destinations ADD COLUMN host_key TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE backup_destinations DROP COLUMN host_key;
ALTER TABLE backup_destinations DROP COLUMN ssh_key_id;
