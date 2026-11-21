# Sessions end after 24 idle hours or 7 days, and are stored as hashes

The panel deploys any Compose YAML, so a signed-in session is root on the
host. A session lasted 30 days from sign-in, whether it was used or not.
`sessions.token` held the cookie value itself, so a leaked `panel.db` or
backup handed out live sessions, and dead rows were never deleted.

**Decision:**

- **A session is live while both rules hold:**
  - it is under 7 days old (`expires_at`, fixed at sign-in)
  - its last request was under 24 hours ago (`last_seen_at`)

  The server enforces both. The cookie carries `Max-Age` 7 days and cannot
  know about the idle rule.
- **`last_seen_at` is written at most every 10 minutes per session.** A
  request writes it only when the stored value is at least 10 minutes old.
  The panel has one SQLite connection, and polling pages send a request every
  few seconds, so writing on every request would queue every other query
  behind it. The cost is that a session can end up to 10 minutes before a
  full 24 idle hours.
- **A polling tab counts as activity.** A tab left open on a page that polls
  keeps its session alive, and the 7-day cap is what bounds it.
- **The table holds the hex SHA-256 of the cookie token** (`token_hash`),
  the same as invite and reset links. Migration `00056` renames the column
  and deletes every session, because a raw token cannot be hashed in SQL.
  Every user, the Owner included, signs in once after the update.
- **Dead sessions are deleted at start and every hour.**
- **The session lookup never runs inside an open transaction.** It writes
  through the one connection, so it runs before a handler's `BeginTx`.

**Not done:**

- asking for the password again before dangerous actions
- a `__Host-` cookie prefix
- a list of a user's sessions
- configurable timeouts
