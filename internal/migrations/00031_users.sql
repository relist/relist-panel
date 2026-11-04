-- +goose Up
-- The single admin row becomes one User of many (.scratch/multi-user/spec.md).
-- admin_credentials pins itself to id 1 with a CHECK that SQLite cannot drop,
-- so the table is rebuilt as users, keeping id 1: it is the Owner. sessions
-- moves from admin_id to user_id, and user_tokens holds invite and reset
-- links, by hash only.
--
-- foreign_keys is ON and cannot be switched off inside goose's transaction,
-- so every row is copied into the new tables before the old ones are dropped:
-- dropping admin_credentials would otherwise cascade into the sessions.
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    email TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NULL,
    is_admin INTEGER NOT NULL DEFAULT 0,
    disabled_at INTEGER NULL,
    last_login_at INTEGER NULL,
    created_at INTEGER NOT NULL,
    totp_secret TEXT NULL,
    totp_pending_secret TEXT NULL,
    totp_last_step INTEGER NULL,
    failed_attempts INTEGER NOT NULL DEFAULT 0,
    locked_until INTEGER NULL,
    password_from_env INTEGER NOT NULL DEFAULT 0
);

INSERT INTO users (id, email, password_hash, is_admin, created_at, totp_secret, totp_pending_secret,
                   totp_last_step, failed_attempts, locked_until, password_from_env)
SELECT id, email, password_hash, 1, created_at, totp_secret, totp_pending_secret,
       totp_last_step, failed_attempts, locked_until, password_from_env
FROM admin_credentials;

CREATE TABLE sessions_new (
    token TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);

INSERT INTO sessions_new (token, user_id, expires_at, created_at)
SELECT token, admin_id, expires_at, created_at FROM sessions;

DROP TABLE sessions;
DROP TABLE admin_credentials;
ALTER TABLE sessions_new RENAME TO sessions;

CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);

CREATE TABLE user_tokens (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('invite', 'reset')),
    token_hash TEXT NOT NULL UNIQUE,
    expires_at INTEGER NOT NULL,
    created_by INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
    used_at INTEGER NULL
);

CREATE UNIQUE INDEX user_tokens_one_open_idx ON user_tokens (user_id) WHERE used_at IS NULL;

-- +goose Down
DROP TABLE user_tokens;

CREATE TABLE admin_credentials (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    email TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    totp_secret TEXT NULL,
    totp_pending_secret TEXT NULL,
    totp_last_step INTEGER NULL,
    failed_attempts INTEGER NOT NULL DEFAULT 0,
    locked_until INTEGER NULL,
    password_from_env INTEGER NOT NULL DEFAULT 0
);

INSERT INTO admin_credentials (id, email, password_hash, created_at, totp_secret, totp_pending_secret,
                               totp_last_step, failed_attempts, locked_until, password_from_env)
SELECT id, email, password_hash, created_at, totp_secret, totp_pending_secret,
       totp_last_step, failed_attempts, locked_until, password_from_env
FROM users
WHERE id = 1 AND password_hash IS NOT NULL;

CREATE TABLE sessions_old (
    token TEXT PRIMARY KEY,
    admin_id INTEGER NOT NULL REFERENCES admin_credentials(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);

INSERT INTO sessions_old (token, admin_id, expires_at, created_at)
SELECT token, user_id, expires_at, created_at FROM sessions WHERE user_id IN (SELECT id FROM admin_credentials);

DROP TABLE sessions;
DROP TABLE users;
ALTER TABLE sessions_old RENAME TO sessions;

CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
