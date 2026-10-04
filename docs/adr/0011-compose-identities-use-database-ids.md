# New Compose identities use immutable database IDs

Name-derived identities in ADR-0002/0005 are ambiguous: `a-b/c/web` and `a/b-c/web` share a Docker project name, and reusing a renamed Project's old name can share both its Docker name and files. New Composes store `identity_version = 1` and use `relist-c<ID>` plus `<data dir>/.composes/<ID>/`; SQLite AUTOINCREMENT keeps an ID from being reused after deletion. A two-part Docker name cannot equal the legacy three-part slug identity, and a dotted directory cannot equal a legacy slug directory.

Migration 00011 leaves existing rows at version 0, preserving their frozen names, paths and running containers. Automatically moving or renaming existing stacks would require a disruptive deployment migration, so it is deliberately avoided. Operations on a legacy identity shared by another row are refused before Docker or filesystem changes; such an existing collision needs operator reconciliation because the panel cannot determine which row owns the shared stack. Read-only status stays best-effort.

Parent deletion tears down child infrastructure before cascading metadata. HTTP requests hold a Project gate across teardown and metadata deletion, child creation, lifecycle mutations and Volume operations; other Projects remain independent. This is a deliberate coarse boundary for the single-admin panel, allowing parent/child ordering without nested locks. Teardown has no transaction over Docker: on failure metadata remains, completed children stay stopped, and a retry continues from the remaining files.

Compose API responses include a read-derived `deployIdentity` (Docker project name and configured directory) so displays and deletion confirmations address the same identity as lifecycle actions, including legacy deployments.
