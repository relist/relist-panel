# 01 — Project scaffold: Go binary + embedded React app boots

**What to build:** A running Go binary that serves the embedded frontend build and proves every layer of the stack — Go compile, embedded SPA, HTTP server, SQLite via goose — is wired together end to end. No domain features yet; this is the walking skeleton everything else builds on.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] `go build` produces a single binary with the Vite/React build output embedded via `go:embed`, in its own `webassets` package (ADR-0003)
- [ ] Running the binary starts an HTTP server on a configurable port using the stdlib `net/http.ServeMux` (ADR-0003) — no external router
- [ ] `GET /healthz` returns 200
- [ ] Visiting the server root in a browser renders the embedded React app's shell (even if empty)
- [ ] SQLite opens at `<data dir>/panel.db` (default `/var/lib/relist-panel`, overridable via `RELIST_DATA_DIR`, ADR-0002) and goose runs its (currently empty) embedded migrations on startup without error
- [ ] Config (port, data dir) is a small hand-rolled struct read from `os.Getenv` — no config library (ADR-0003)
