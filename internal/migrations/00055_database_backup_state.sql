-- +goose Up
-- Review 2026-11-20 (ADR-0050): what a one-database dump leaves out, read
-- when the Backup is taken, as JSON: the database's owner and ACL (Postgres)
-- or character set and collation (MySQL, MariaDB), and each login user's
-- access to it. A restore into a new database, and Recreate after a lost
-- volume, create the database as it was backed up. Older Backups have ''.
ALTER TABLE database_backups ADD COLUMN state TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE database_backups DROP COLUMN state;
