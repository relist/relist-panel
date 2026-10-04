-- +goose Up
-- Two-factor state and the sign-in lock live on the single admin row. The
-- whole set lands here so later tickets do not need another migration:
-- totp_secret carries the confirmed base32 secret (NULL means two-factor is
-- off), totp_pending_secret holds a setup that has not been confirmed yet,
-- and totp_last_step is the 30-second step of the last accepted code so a
-- code cannot be replayed. failed_attempts counts consecutive failures and
-- locked_until (unix seconds) freezes every credential check while it is in
-- the future.
ALTER TABLE admin_credentials ADD COLUMN totp_secret TEXT NULL;
ALTER TABLE admin_credentials ADD COLUMN totp_pending_secret TEXT NULL;
ALTER TABLE admin_credentials ADD COLUMN totp_last_step INTEGER NULL;
ALTER TABLE admin_credentials ADD COLUMN failed_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE admin_credentials ADD COLUMN locked_until INTEGER NULL;

-- +goose Down
ALTER TABLE admin_credentials DROP COLUMN locked_until;
ALTER TABLE admin_credentials DROP COLUMN failed_attempts;
ALTER TABLE admin_credentials DROP COLUMN totp_last_step;
ALTER TABLE admin_credentials DROP COLUMN totp_pending_secret;
ALTER TABLE admin_credentials DROP COLUMN totp_secret;
