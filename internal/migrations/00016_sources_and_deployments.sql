-- +goose Up
CREATE TABLE compose_sources (
    compose_id INTEGER PRIMARY KEY REFERENCES composes(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('git', 'zip')),
    repo_url TEXT NOT NULL DEFAULT '',
    branch TEXT NOT NULL DEFAULT '',
    submodules INTEGER NOT NULL DEFAULT 0 CHECK (submodules IN (0, 1)),
    ssh_private_key TEXT NOT NULL DEFAULT '',
    ssh_public_key TEXT NOT NULL DEFAULT '',
    last_test_at INTEGER,
    last_test_ok INTEGER,
    head_sha TEXT NOT NULL DEFAULT '',      -- branch head from the last test
    zip_name TEXT NOT NULL DEFAULT '',
    zip_size INTEGER NOT NULL DEFAULT 0,
    zip_files INTEGER NOT NULL DEFAULT 0,
    uploaded_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE deployments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    compose_id INTEGER NOT NULL REFERENCES composes(id) ON DELETE CASCADE,
    number INTEGER NOT NULL,
    mode TEXT NOT NULL CHECK (mode IN ('deploy', 'build', 'skip-build')),
    no_cache INTEGER NOT NULL DEFAULT 0,
    fresh_volumes INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'canceled')),
    step TEXT NOT NULL DEFAULT '',          -- current or last step: fetch, build:<svc>, deploy
    steps TEXT NOT NULL DEFAULT '[]',       -- JSON: planned steps, fixed when the run starts
    failed_step TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',        -- timeout, panel restarted, or the error's first line
    source_ref TEXT NOT NULL DEFAULT '',    -- git SHA, or the zip's name
    source_subject TEXT NOT NULL DEFAULT '',
    warnings TEXT NOT NULL DEFAULT '[]',
    queued_at INTEGER NOT NULL,
    started_at INTEGER,
    finished_at INTEGER,
    UNIQUE (compose_id, number)
);
CREATE INDEX deployments_status_idx ON deployments(status, queued_at);
CREATE UNIQUE INDEX deployments_one_queued ON deployments(compose_id) WHERE status = 'queued';

-- +goose Down
DROP TABLE deployments;
DROP TABLE compose_sources;
