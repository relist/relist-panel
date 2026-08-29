# 06 — Environment Variables

**What to build:** Within an Environment, an operator can manage a set of key-value Environment Variables, scoped to that Environment and shared by every Compose inside it.

**Blocked by:** 05

**Status:** closed

- [x] A migration creates the `environment_variables` table (id, environment_id FK, key, value), unique on (environment_id, key), per `docs/sa.md`
- [x] The Environment Variables sub-tab (per `EnvironmentVariables.dc.html`) lists all variables for the active Environment
- [x] An operator can add, edit, and delete a variable
- [x] Covered by HTTP-seam tests per the spec's Testing Decisions

## Comments

Implemented Ticket 06 in the working tree with `internal/migrations/00005_environment_variables.sql` and the new `internal/environmentvariables` domain package. Environment Variables use integer IDs, belong to an Environment with foreign-key cascade behavior, and enforce unique keys within each Environment. Authenticated HTTP routes support list, create, edit, and delete operations at the nested Project → Environment path; edits update both key and value and return 409 on a key collision.

The Project detail UI now renders an Overview and Environment Variables sub-tab for each Environment. The variables panel supports loading, empty, error, add, edit, and delete states using the existing Ant Design components. Values are displayed as plain text, consistent with the v1 scope's lack of a separate secrets tier. The referenced `EnvironmentVariables.dc.html` mockup was not present, so the UI follows the existing Ant Design and `DESIGN.md` tokens.

HTTP-seam tests use real SQLite migrations and `httptest` requests, covering scoping, parent validation, duplicate keys, key renames, updates, and deletion. Verified with `pnpm --dir web typecheck`, `make test`, `go test -race ./...`, `go vet ./...`, `go build -o /tmp/relist-panel-ticket06 .`, and `git diff --check`.

The handoff requested separate local implementation and closure commits, but Git staging is currently blocked by the sandbox's read-only `.git` mount (`Unable to create .git/index.lock`). No remote or branch was changed; the source and ticket updates remain available for staging and committing in a writable Git environment.
