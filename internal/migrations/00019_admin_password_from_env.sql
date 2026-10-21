-- +goose Up
-- Whether the admin's password is still the one RELIST_ADMIN_PASSWORD seeded,
-- so the account page need not read the env or run bcrypt (ADR-0003).
ALTER TABLE admin_credentials ADD COLUMN password_from_env INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE admin_credentials DROP COLUMN password_from_env;
