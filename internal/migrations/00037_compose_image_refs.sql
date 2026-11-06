-- +goose Up
-- The image references each Compose's latest successful deploy pulled or
-- built (.scratch/private-registry/spec.md). Registry cleanup never deletes a
-- tag listed here, and Disable warns about the Composes that use the Registry.
CREATE TABLE compose_image_refs (
    compose_id INTEGER NOT NULL REFERENCES composes(id) ON DELETE CASCADE,
    ref TEXT NOT NULL,
    PRIMARY KEY (compose_id, ref)
);

-- +goose Down
DROP TABLE compose_image_refs;
