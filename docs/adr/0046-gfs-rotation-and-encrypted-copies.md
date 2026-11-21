# GFS Rotation, and encrypted Backup copies

Phase 9 (`.scratch/databases/plan-phase-9.md`) adds calendar Rotation tiers
and lets a Backup destination encrypt the copies it receives.

**Decision:**

- **GFS is a mode on the pure `Rotate` function.** Each Rotation set (Local,
  Destination) picks Simple (today's keep last N / at most X days) or GFS
  (migration `00052`); an absent mode reads as Simple, so existing schedules
  are untouched. In GFS an item is **kept when any rule keeps it**: the
  newest successful Backup of its group (the rule both modes share), last-N
  by count, or being the newest **successful** item of a calendar day, ISO
  week, month or year within the last D/W/M/Y buckets. Failed Backups never
  fill a tier slot, though they still occupy count slots exactly as in
  Simple. Buckets are calendar buckets in the host's time zone (the clock
  the scheduler passes), so 23:59 and 00:01 are different days. `keep_count`
  doubles as GFS's "last N"; "at most X days" is Simple-only and cleared on
  a GFS save. A GFS set with no count and no tiers would delete everything
  but the newest success, so it is refused ("keep at least one tier").
- **Copies encrypt as age files with an scrypt passphrase**
  (`filippo.io/age`). Only copies to a destination with **Encrypt copies**
  on are encrypted — local Backups stay plain so they restore directly. A
  copy is `<object key>.age` and opens anywhere with `age -d`.
- **Work factor 2^15, one derivation at a time.** age's default 2^18 costs
  256 MiB per derivation; the panel container runs under
  `mem_limit: 256M` / `GOMEMLIMIT: 200MiB` and would be killed. 2^15 is
  32 MiB and about 0.1 s. A package mutex in `internal/agefile` serializes
  every derivation in the panel, and a test pins the memory one derivation
  adds.
- **Encrypt then upload.** The copy is encrypted into a temp file beside the
  Backup (`*.age.tmp`, 0600), uploaded with the encrypted file's exact size
  through the same stall-watchdog path, and the temp removed whatever
  happens. Each upload row records `encrypted`.
- **The passphrase is stored, revealed only audited, forgotten when off.**
  Scheduled uploads need it, so it lives in `backup_destinations.passphrase`
  (its own column, a secret like the destination's secret: scrubbed from
  every response, error and audit detail). The form generates 24 base58
  characters client-side (`crypto.getRandomValues`, ≈140 bits) or takes a
  typed one of at least 20 characters, and says in bold that the panel
  cannot help if the server is lost. `GET
  /api/backup-destinations/{id}/passphrase` reveals it, audited as
  `secret.read`. Changing the passphrase or turning encryption off applies
  to new copies only; the dialog says existing copies keep the old
  passphrase and the panel forgets it. A retried copy is a new copy and uses
  the destination's current settings.
- **Restore from file detects `.age` by its binary header** and asks for the
  passphrase, sent in an `X-Passphrase` header (never the query string,
  which can land in access logs). The decrypted stream goes through the same
  upload check as a plain file. A header asking for a work factor above 2^15
  is refused **before any derivation** ("decrypt it with `age -d` first"),
  so an upload cannot make the panel allocate gigabytes; a test pins that
  no allocation happens. Armored age files are not detected and fall
  through to the SQL check.

**Considered and rejected:**

- *Per-tier cron expressions* — far more surface than D/W/M/Y counts, and
  the spec's buckets answer the actual need.
- *Encrypting local Backups too* — a local restore must not depend on a
  passphrase; losing it would strand every Backup on the server itself.
- *X25519 recipients instead of a passphrase* — the identity would have to
  live somewhere safer than the panel database, and there is no such place
  in a single-server panel.

**Consequences:** copies in a bucket need `age -d` (or the panel) to open; a
lost passphrase loses every encrypted copy, which the UI says in bold;
interop is proven in e2e with typage (the age author's TypeScript
implementation), whose default work factor 2^18 also exercises the refusal
path.
