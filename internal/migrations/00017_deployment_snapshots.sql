-- +goose Up
-- Private execution inputs deliberately live outside every history/API query.
CREATE TABLE deployment_snapshots (
    deployment_id INTEGER PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    snapshot TEXT NOT NULL
);
CREATE TABLE compose_source_materializations (
    compose_id INTEGER PRIMARY KEY REFERENCES composes(id) ON DELETE CASCADE,
    source_dir TEXT NOT NULL
);

-- +goose Down
DROP TABLE deployment_snapshots;
DROP TABLE compose_source_materializations;
