-- +goose Up
-- Variable Sets move from the Environment to the Compose. Each Environment set
-- is copied into every Compose of its Environment, entries included, and each
-- binding is pointed at its own Compose's copy. A set in an Environment with
-- no Compose has nowhere to go and is dropped.
--
-- foreign_keys is ON and cannot be switched off inside goose's transaction,
-- so every row is copied into the new tables before the old ones are dropped:
-- the cascades those drops fire only reach rows already copied.
CREATE TABLE variable_sets_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    compose_id INTEGER NOT NULL REFERENCES composes(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (compose_id, name)
);

CREATE TABLE variable_set_entries_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    set_id INTEGER NOT NULL REFERENCES variable_sets_new(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    secret INTEGER NOT NULL DEFAULT 0,
    UNIQUE (set_id, key)
);

CREATE TABLE service_bindings_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    compose_id INTEGER NOT NULL REFERENCES composes(id) ON DELETE CASCADE,
    service TEXT NOT NULL,
    set_id INTEGER NOT NULL REFERENCES variable_sets_new(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    UNIQUE (compose_id, service, set_id)
);

INSERT INTO variable_sets_new (compose_id, name, created_at, updated_at)
SELECT c.id, s.name, s.created_at, s.updated_at
FROM variable_sets s
JOIN composes c ON c.environment_id = s.environment_id
ORDER BY s.id, c.id;

-- Set names are unique per Environment, so (compose_id, name) finds the copy.
INSERT INTO variable_set_entries_new (set_id, key, value, secret)
SELECT n.id, e.key, e.value, e.secret
FROM variable_set_entries e
JOIN variable_sets s ON s.id = e.set_id
JOIN composes c ON c.environment_id = s.environment_id
JOIN variable_sets_new n ON n.compose_id = c.id AND n.name = s.name
ORDER BY n.id, e.id;

INSERT INTO service_bindings_new (compose_id, service, set_id, position)
SELECT b.compose_id, b.service, n.id, b.position
FROM service_bindings b
JOIN variable_sets s ON s.id = b.set_id
JOIN variable_sets_new n ON n.compose_id = b.compose_id AND n.name = s.name
ORDER BY b.id;

DROP TABLE service_bindings;
DROP TABLE variable_set_entries;
DROP TABLE variable_sets;

ALTER TABLE variable_sets_new RENAME TO variable_sets;
ALTER TABLE variable_set_entries_new RENAME TO variable_set_entries;
ALTER TABLE service_bindings_new RENAME TO service_bindings;

CREATE INDEX variable_sets_compose_id_idx ON variable_sets(compose_id);
CREATE INDEX variable_set_entries_set_id_idx ON variable_set_entries(set_id);
CREATE INDEX service_bindings_compose_id_idx ON service_bindings(compose_id);
CREATE INDEX service_bindings_set_id_idx ON service_bindings(set_id);

-- +goose Down
-- Lossy: sets of the same name in one Environment collapse into the copy with
-- the lowest id, and its entries are the ones kept.
CREATE TABLE variable_sets_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    environment_id INTEGER NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (environment_id, name)
);

CREATE TABLE variable_set_entries_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    set_id INTEGER NOT NULL REFERENCES variable_sets_old(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    secret INTEGER NOT NULL DEFAULT 0,
    UNIQUE (set_id, key)
);

CREATE TABLE service_bindings_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    compose_id INTEGER NOT NULL REFERENCES composes(id) ON DELETE CASCADE,
    service TEXT NOT NULL,
    set_id INTEGER NOT NULL REFERENCES variable_sets_old(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    UNIQUE (compose_id, service, set_id)
);

INSERT INTO variable_sets_old (id, environment_id, name, created_at, updated_at)
SELECT s.id, c.environment_id, s.name, s.created_at, s.updated_at
FROM variable_sets s
JOIN composes c ON c.id = s.compose_id
WHERE s.id = (
    SELECT MIN(s2.id) FROM variable_sets s2
    JOIN composes c2 ON c2.id = s2.compose_id
    WHERE c2.environment_id = c.environment_id AND s2.name = s.name
);

INSERT INTO variable_set_entries_old (set_id, key, value, secret)
SELECT e.set_id, e.key, e.value, e.secret
FROM variable_set_entries e
WHERE e.set_id IN (SELECT id FROM variable_sets_old)
ORDER BY e.id;

INSERT INTO service_bindings_old (compose_id, service, set_id, position)
SELECT b.compose_id, b.service, o.id, b.position
FROM service_bindings b
JOIN variable_sets s ON s.id = b.set_id
JOIN composes c ON c.id = b.compose_id
JOIN variable_sets_old o ON o.environment_id = c.environment_id AND o.name = s.name
ORDER BY b.id;

DROP TABLE service_bindings;
DROP TABLE variable_set_entries;
DROP TABLE variable_sets;

ALTER TABLE variable_sets_old RENAME TO variable_sets;
ALTER TABLE variable_set_entries_old RENAME TO variable_set_entries;
ALTER TABLE service_bindings_old RENAME TO service_bindings;

CREATE INDEX variable_sets_environment_id_idx ON variable_sets(environment_id);
CREATE INDEX variable_set_entries_set_id_idx ON variable_set_entries(set_id);
CREATE INDEX service_bindings_compose_id_idx ON service_bindings(compose_id);
CREATE INDEX service_bindings_set_id_idx ON service_bindings(set_id);
