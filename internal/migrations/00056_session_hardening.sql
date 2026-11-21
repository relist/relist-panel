-- Session hardening (.scratch/session-hardening/spec.md, ADR-0051). Sessions
-- are looked up by the SHA-256 of their cookie token and end after 24 idle
-- hours or 7 days. Raw tokens cannot be hashed in SQL, so every session ends
-- here and each user signs in once after the update.

-- +goose Up
DELETE FROM sessions;
ALTER TABLE sessions RENAME COLUMN token TO token_hash;
ALTER TABLE sessions ADD COLUMN last_seen_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
DELETE FROM sessions;
ALTER TABLE sessions DROP COLUMN last_seen_at;
ALTER TABLE sessions RENAME COLUMN token_hash TO token;
