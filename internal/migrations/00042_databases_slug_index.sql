-- +goose Up
-- A Database's readable alias is <slug>.db.internal on relist-network, so two
-- Databases must not share a slug (ADR-0039). The Service checks first; this
-- index closes the race between two concurrent creates or renames. The
-- fallback slug "x" (a name with no ASCII letters or digits) has no alias and
-- may repeat.
CREATE UNIQUE INDEX databases_slug ON databases (slug) WHERE slug <> 'x';

-- +goose Down
DROP INDEX databases_slug;
