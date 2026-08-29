# 03 — Auth: setup admin, login, session

**What to build:** An operator can set up the single admin account on first run, log in, stay logged in across reloads, and log out. Every other route requires authentication.

**Blocked by:** 01

**Status:** closed

- [x] A migration creates a table for the single admin credential (email + bcrypt-hashed password) — new table, not part of the original 4-table domain model in `docs/sa.md`, since auth is infrastructure rather than domain
- [x] With no admin account yet, visiting the app shows the Setup screen (per `Setup.dc.html`); submitting valid credentials creates the admin account
- [x] Once an admin account exists, visiting the app shows the Login screen (per `Login.dc.html`) instead of Setup
- [x] A successful login establishes a session (cookie/token) that persists across browser reloads
- [x] Logging out clears the session and returns to the Login screen
- [x] All non-auth routes are rejected without a valid session (middleware-enforced)
- [x] Covered by HTTP-seam tests per the spec's Testing Decisions

## Comments

Implemented the single-admin auth flow with `internal/migrations/00002_auth.sql` (`admin_credentials` and database-backed `sessions`), bcrypt password hashing, 30-day opaque session tokens, and an HttpOnly/SameSite=Lax cookie. The cookie is marked Secure for TLS or HTTPS-forwarded requests while remaining usable over local HTTP development. `internal/auth` owns the credential/session behavior and middleware; `internal/httpapi.NewHandler` composes it with the existing health and embedded-frontend routes. Setup is one-time and returns a conflict after the singleton admin row exists; login and logout create/revoke sessions explicitly.

The referenced `Setup.dc.html` and `Login.dc.html` mockups were not present in the repository, so the screens were implemented directly with the established Ant Design + Tailwind setup: centered cards, email/password forms, setup confirmation, clear success/error states, and a minimal authenticated landing state reserved for the Projects ticket.

HTTP-seam tests use real SQLite migrations and `httptest` requests, covering setup state transitions, bcrypt persistence, duplicate setup rejection, invalid credentials, session persistence, middleware rejection, and logout revocation. Verified with `pnpm --dir web typecheck`, `make test`, `go build`, `go vet ./...`, `go test -race ./...`, and `git diff --check`.
