# Session hardening

Status: ready-for-agent

## Problem

The panel can deploy any Compose YAML, which is root on the host. Its sign-in
session is weaker than that warrants:

- **One fixed 30-day lifetime.** `sessionLifetime = 30 * 24 * time.Hour`
  (`internal/auth/auth.go`). A session left signed in on a borrowed machine,
  or a stolen cookie, stays good for a month whether it is used or not.
- **Raw tokens in the database.** `sessions.token` holds the cookie value
  itself. A leaked `panel.db` or backup hands out live sessions. Invite and
  reset links are already stored as SHA-256 (`hashToken`, `internal/auth/tokens.go`);
  sessions are not.
- **Dead rows are never removed.** Expired sessions stay in the table forever.

## Decisions

| Rule | Value |
|---|---|
| Idle timeout: a session with no request for this long ends | **24 hours** |
| Absolute lifetime: a session ends this long after sign-in, however active | **7 days** |
| `last_seen_at` is written at most once per session per | **10 minutes** |
| Session token storage | hex SHA-256 of the cookie value, same as `hashToken` |
| Dead-session cleanup | at start, then every **1 hour** |

### Behaviour

- A session is live when **both** hold, with `now` in Unix seconds:
  - `expires_at > now`, where `expires_at = created_at + 7 days`, fixed at sign-in
  - `last_seen_at + 24h > now`
- Every authenticated request (`Middleware`, `CurrentUser`, `GET /api/auth/status`,
  the account and two-factor routes) applies the same rule through one lookup.
- **Touching a session:** when a live session's `now - last_seen_at >= 10 minutes`,
  the request sets `last_seen_at = now`. Otherwise nothing is written.
  - A failed touch is logged and the request still goes on, because the session is live.
  - The effect is that a session can end up to 10 minutes before a full 24 idle hours.
    This is accepted.
- **An open tab can count as activity.** Polling is activity. For example,
  `useDiskPressure` reads host metrics every 10 s whenever it is enabled. A tab
  open on a polling page keeps its session alive until the 7-day cap. This is
  intended; the cap is what bounds it.
- **Cookie:** `Max-Age` = 7 days, `Expires` = the session's `expires_at`. The
  server, not the cookie, enforces the idle rule.
- **Upgrade:** the migration deletes every existing session. Every user signs
  in once after updating. Raw tokens cannot be hashed in SQL, and a one-time
  sign-in is cheaper than a Go-side conversion.
- **Unchanged:** sessions are still revoked on logout, password change, reset,
  disable, delete, invite acceptance and two-factor enable/disable, exactly as now.
  This also covers keeping the current session on a password or two-factor change.

## Out of scope

- Asking for the password again before dangerous actions
- `__Host-` cookie prefix
- A "your sessions" list or "sign out everywhere" UI
- Making the timeouts configurable
