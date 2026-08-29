# 01 — Project scaffold: Go binary + embedded React app boots

**What to build:** A running Go binary that serves the embedded frontend build and proves every layer of the stack — Go compile, embedded SPA, HTTP server, SQLite via goose — is wired together end to end. No domain features yet; this is the walking skeleton everything else builds on.

**Blocked by:** None — can start immediately.

**Status:** closed

- [x] `go build` produces a single binary with the Vite/React build output embedded via `go:embed`, in its own `webassets` package (ADR-0003)
- [x] Running the binary starts an HTTP server on a configurable port using the stdlib `net/http.ServeMux` (ADR-0003) — no external router
- [x] `GET /healthz` returns 200
- [x] Visiting the server root in a browser renders the embedded React app's shell (even if empty)
- [x] SQLite opens at `<data dir>/panel.db` (default `/var/lib/relist-panel`, overridable via `RELIST_DATA_DIR`, ADR-0002) and goose runs its (currently empty) embedded migrations on startup without error
- [x] Config (port, data dir) is a small hand-rolled struct read from `os.Getenv` — no config library (ADR-0003)

## Comments

Implemented in `d7828b6`. `/code-review` against that commit found two hard findings (missing `go.sum` breaking a clean `go build`; HTTP-seam tests living in `package main` instead of an external test package per ADR-0003) plus a minor Duplicated Code smell (build command declared in both the Makefile and `//go:generate`) — all fixed in `52029fa`. Verified end to end: `make build` && `make test` pass, and the built binary serves `/healthz` (200), serves the embedded frontend shell at `/`, and creates `panel.db` with goose's migration applied.
