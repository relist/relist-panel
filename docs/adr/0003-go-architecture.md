# Go architecture: domain packages, single port, stdlib-first

The Go backend needed a folder structure and set of design patterns that stay easy to navigate for a solo/small-team project, without adopting layering (Clean Architecture, full Hexagonal) that doesn't pay for itself at this app's size — 4 database tables, and exactly one external system (Docker) genuinely worth abstracting for tests.

- **Package-per-domain**, not per-layer: `project/`, `environment/`, `compose/`, `auth/` under `internal/`, each owning its own model, SQLite-backed store, and HTTP handlers together. Package names mirror `CONTEXT.md` vocabulary exactly.
- **Exactly one port/adapter pair in the whole system**: the `ComposeRunner` interface (`Up`/`Down`/`Restart`/`Status`/`Logs`), declared inside `compose` (the consumer) rather than beside its implementation. The real shell-exec-to-`docker compose` implementation lives in its own leaf package, `dockerexec`, wired together only in the composition root (`main.go`). This is the one dependency worth swapping for a fake in tests (see the `to-spec` HTTP-seam decision); everywhere else — SQLite included — is used concretely, with no interface wrapping it, since nothing else is ever going to be swapped out.
- **HTTP routing**: stdlib `net/http.ServeMux` (Go 1.22+ method+path patterns). No chi/gorilla dependency.
- **SQLite access**: hand-written `database/sql` + small repository methods per domain package. No ORM, no sqlc codegen — 4 tables doesn't justify the tooling.
- **Migrations**: goose from day one, not a deferred idempotent `schema.sql`. `.sql` migration files are embedded via `go:embed` and run automatically (`goose.Up()`) from `main.go` on startup — chosen so schema changes after launch (with real data already in the tables) have a safe, versioned path immediately, rather than a retrofit once it's painful.
- **Config**: a small hand-rolled `Config` struct populated from `os.Getenv`. No viper/envconfig.
- **Tests**: HTTP-seam tests are written as external test packages (e.g. `compose_test`) so they can only exercise exported behavior, enforcing the black-box HTTP seam.
- **Frontend embedding**: the Vite/React build output is `go:embed`-ed from its own package, `webassets`, kept separate from the routing/middleware package so each package has one job.
