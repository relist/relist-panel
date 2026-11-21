# SFTP Backup destinations: a host key trusted on first Test

ADR-0040 made a Backup destination a named, host-wide entry with one `type`, `s3`. Phase 6a adds a second, `sftp`, for a Hetzner Storage Box, a Synology or any OpenSSH server (`.scratch/databases/plan-phase-6a.md`). Every S3 rule of ADR-0040 holds: the object key layout, deleting only recorded copies, the size check after upload, the 5-minute stall cut, the 1-minute Delete and Test timeouts, and the secret rules.

**Decision:**
- **Trust on first use, at Test only.** The first successful Test records the host key the server presents; every later connection must present the same key or fails with "the server's host key changed". An upload never records a key, so a destination that has never passed Test cannot be used by a schedule. The key lives in its own column (`backup_destinations.host_key`, one `authorized_keys` line), not in `config`, so recording it never looks like a settings change. It is forgotten when the host or the port changes, and by **Reset host key**, which also forgets the last Test result. Keys are compared by their marshalled bytes, never by fingerprint text.
- **The recorded algorithm is pinned.** OpenSSH offers ed25519, ECDSA and RSA keys. Once a key is recorded the client asks only for that key's algorithm (`rsa-sha2-512`, `rsa-sha2-256`, `ssh-rsa` for RSA), or the server may present another of its keys and every upload would fail with a false "host key changed".
- **A lazy connection per operation.** `Put`, `Delete` and `Test` each open one SSH connection and close it. `Destinations.Open` is also called only to read a name (the schedule save, an error message), and that must never log in. Neither `pkg/sftp` nor `x/crypto/ssh` honours a context, so the connection is closed when the context ends; that is what enforces the stall cut and the timeouts, from the dial to the last byte.
- **`.part`, then rename.** An upload writes `<key>.part`, checks its size, then renames it to `<key>` (`posix-rename@openssh.com` when the server has it, else remove and rename), so a final name never holds a partial file. A failed upload removes the `.part`.
- **Authentication** is a key from the SSH keys library (recommended) or a password; the password is stored like an S3 secret and asked again when the host or port changes, or when the auth switches from key to password.
- **A used SSH key cannot be deleted.** The SSH keys page lists the destinations using a key, and deleting one answers 409 naming them; the column is `ON DELETE RESTRICT` as a backstop. Compose sources keep today's behaviour, where deleting their key leaves the Source with none.

## Considered options

**Recording the host key on any connection.** Rejected: an upload in the middle of the night would silently trust whatever answered.

**Comparing fingerprints as text.** Rejected: bytes are the key; a label is only for people.

**Keeping one connection per destination.** Rejected: a long-lived connection needs keepalives and reconnect logic, and a Backup runs at most once a day per Database.

## Consequences

- A host key is trusted without out-of-band verification, so the form shows the fingerprint to compare with the server's own.
- The panel holds SFTP passwords like S3 secrets (ADR-0040).
- A server that is replaced or reinstalled fails its uploads until an Admin uses Reset host key and Test again.
