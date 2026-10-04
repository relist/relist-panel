-- The secret flag is a display tier owned by the user (spec,
-- variable-secrets): a marked value is masked on screen, and that is all the
-- flag does — the value stays stored and served exactly as a plain one. The
-- column lands as 0 (plain) and the backfill below marks the rows that
-- already exist with the UI's key-shape heuristic, so the flag starts as the
-- guess the screens were making anyway. From then on the flag is a stored
-- fact the user can correct; no Go-side guessing repeats after this.

-- +goose Up
ALTER TABLE environment_variables ADD COLUMN secret INTEGER NOT NULL DEFAULT 0;

-- One-time backfill: the SQL translation of the web UI's SECRET_KEY_PATTERN
-- (web/src/components/variables-view.ts),
-- /secret|password|passwd|token|credential|(^|_)(api_?)?key($|_)|(^|_)private_?key/i.
-- GLOB is case-sensitive, so it runs on lower(key) — keys are validated to
-- ASCII letters, digits and underscores, which lower() maps exactly. `_` is
-- literal in GLOB (unlike LIKE), which is what makes the word boundaries of
-- the key family expressible: `key`, `apikey` and `api_key` count only as
-- whole underscore-delimited words (HONEYPOT_KEY matches, MONKEY_PATCH and
-- KEYCLOAK_URL do not), while the private-key family has no trailing
-- boundary in the pattern, so PRIVATE_KEY_PATH matches.
UPDATE environment_variables
SET secret = 1
WHERE lower(key) GLOB '*secret*'
   OR lower(key) GLOB '*password*'
   OR lower(key) GLOB '*passwd*'
   OR lower(key) GLOB '*token*'
   OR lower(key) GLOB '*credential*'
   OR lower(key) GLOB 'key'
   OR lower(key) GLOB 'key_*'
   OR lower(key) GLOB '*_key'
   OR lower(key) GLOB '*_key_*'
   OR lower(key) GLOB 'apikey'
   OR lower(key) GLOB 'apikey_*'
   OR lower(key) GLOB '*_apikey'
   OR lower(key) GLOB '*_apikey_*'
   OR lower(key) GLOB 'api_key'
   OR lower(key) GLOB 'api_key_*'
   OR lower(key) GLOB '*_api_key'
   OR lower(key) GLOB '*_api_key_*'
   OR lower(key) GLOB 'privatekey*'
   OR lower(key) GLOB '*_privatekey*'
   OR lower(key) GLOB 'private_key*'
   OR lower(key) GLOB '*_private_key*';

-- +goose Down
ALTER TABLE environment_variables DROP COLUMN secret;
