# 04 — Projects: CRUD + app shell

**What to build:** A logged-in operator sees the app's navigation shell and can create, list, edit, and delete Projects.

**Blocked by:** 03

**Status:** closed

- [x] A migration creates the `projects` table (id, name, description, created_at) per `docs/sa.md`'s Data Dictionary
- [x] The app shell (Sider + Menu, per `Main.dc.html`) renders on every authenticated page from this ticket onward
- [x] The Projects list page shows all Projects as cards (name, description, environment count, last-updated)
- [x] An operator can create a Project (name + description)
- [x] An operator can edit a Project's name/description
- [x] An operator can delete a Project
- [x] Covered by HTTP-seam tests per the spec's Testing Decisions

## Comments

Implemented in `18694ea` with `internal/migrations/00003_projects.sql` and the new `internal/projects` domain package. Projects use integer SQLite IDs to stay consistent with the existing migrations; names are unique case-insensitively, descriptions are nullable, and `updated_at` was added alongside `created_at` so the Projects cards can show a real last-updated timestamp. The derived data dictionary in `docs/sa.md` was updated to match.

The authenticated app now has an Ant Design `Sider` + `Menu` shell, Projects cards, an Open detail view, modal create/edit forms, and confirmed delete actions. The referenced `Main.dc.html` mockup was not present, so the shell follows the established Ant Design and `DESIGN.md` tokens directly. Environment count is `0` until ticket 05 introduces the Environment table. Project deletion is intentionally plain in this ticket; cascading Environment/Compose teardown remains with the later domain and Docker-runner work.

Project CRUD and auth protection are covered through the HTTP seam with real SQLite migrations and `httptest` requests. Verified with `pnpm --dir web typecheck`, `make test`, `go build -o /tmp/relist-panel-ticket04 .`, `go vet ./...`, `go test -race ./...`, and `git diff --check`. The implementation and closure are kept in separate local commits; no remote was configured or changed.
