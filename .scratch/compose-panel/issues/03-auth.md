# 03 — Auth: setup admin, login, session

**What to build:** An operator can set up the single admin account on first run, log in, stay logged in across reloads, and log out. Every other route requires authentication.

**Blocked by:** 01

**Status:** ready-for-agent

- [ ] A migration creates a table for the single admin credential (email + bcrypt-hashed password) — new table, not part of the original 4-table domain model in `docs/sa.md`, since auth is infrastructure rather than domain
- [ ] With no admin account yet, visiting the app shows the Setup screen (per `Setup.dc.html`); submitting valid credentials creates the admin account
- [ ] Once an admin account exists, visiting the app shows the Login screen (per `Login.dc.html`) instead of Setup
- [ ] A successful login establishes a session (cookie/token) that persists across browser reloads
- [ ] Logging out clears the session and returns to the Login screen
- [ ] All non-auth routes are rejected without a valid session (middleware-enforced)
- [ ] Covered by HTTP-seam tests per the spec's Testing Decisions
