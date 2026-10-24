-- +goose Up
-- The file each applied version came from. goose records only the version number,
-- so two branches that both ship a 00020_* cannot be told apart without this.
CREATE TABLE migration_files (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    sha256 TEXT NOT NULL
);

-- +goose Down
DROP TABLE migration_files;
