# Databases Phase 9 Implementation Plan — GFS Rotation (9a) and Backup encryption (9b)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Each Rotation set (Local, Destination) can switch from Simple to GFS tiers, and a Backup destination can encrypt its copies with an age scrypt passphrase that also opens in `age -d`.

**Architecture:** 9a extends the pure `Rotate` function and the schedule row with a per-set mode (`simple`/`gfs`) and D/W/M/Y tier fields; every caller (local rotation, remote rotation) picks it up through the extended `Keep`. 9b adds a small `internal/agefile` package around `filippo.io/age` (scrypt, work factor 2^15, one derivation at a time), an `encrypt` + `passphrase` pair on `backup_destinations`, encrypt-then-upload in `Service.upload`, and `.age` detection + passphrase in Restore from file.

**Tech Stack:** Go, SQLite (goose migration 00052), `filippo.io/age` v1.2.1, React + antd, Playwright (+ `age-encryption` npm package in e2e).

**Spec:** `.scratch/databases/spec-2.md` — "Phase 9: Rotation tiers and encryption" and the Testing section's phase-9 line. Read it first.

**Branch:** `claude/databases-phase-9` from `claude/databases-phase-8`. The PR goes against `claude/databases-phase-8`.

## Global Constraints

- Never edit a committed migration; all schema change is the new `00051`+1 = `internal/migrations/00052_gfs_and_encrypted_copies.sql`. Verify Down→Up once on a scratch DB.
- The passphrase is a secret like a destination's secret access key: it never appears in any response (except the audited reveal), log line, error text, audit detail or e2e output. Scrub it wherever `Destination.Secret` is scrubbed.
- age scrypt work factor is exactly 2^15 on encrypt, and the hard maximum on decrypt. Only one scrypt derivation runs at a time in the whole panel (package-level mutex in `agefile`).
- Local Backups stay plain; only copies to a destination with encryption on are encrypted.
- Existing schedules keep working unchanged: an absent/empty mode means `simple`, and `DefaultSchedule` stays Simple.
- Before touching `web/`, read https://ant.design/llms.txt and the pages for the components used (Radio, InputNumber, Checkbox, Input.Password, Modal).
- Frontend checks: `pnpm exec tsc -b --noEmit`, `pnpm vitest run`, `pnpm build` (there is no `pnpm lint`).
- Conventional Commits, no scope; commits end with `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.
- Schema-version pins in `internal/database/*_test.go` move 51→52 in Task 1.

## Review Focus

Spec-implied conditions no task would otherwise test — each line's test is added to the owning task:

1. **Day buckets at midnight in the host's timezone:** a Backup at 23:59 and one at 00:01 are different GFS days; `now` just after midnight still keeps yesterday's newest. → Task 2 table rows.
2. **GFS with every field blank deletes everything but the newest success.** A save in GFS mode with no Count and no tier set is refused (`keep at least one tier`), so a slip of the form cannot silently erase a year of Backups. → Task 3 test.
3. **The uploaded size is the encrypted file's exact size,** not the plain file's: the fake remote asserts `size == len(encrypted bytes)`, and a failed encryption records `UploadFailed` and leaves no `*.age.tmp` behind. → Task 6 tests.
4. **A wrong passphrase on Restore from file** answers a clean 422 naming the passphrase, and leaves no temp file in `uploads/`. → Task 7 test.
5. **An age file with a work factor above 2^15** is refused before any derivation — the test proves no 4 GiB allocation happens for a 2^22 header. → Task 4 test.

---

### Task 1: Migration 00052 and the three stores

**Files:**
- Create: `internal/migrations/00052_gfs_and_encrypted_copies.sql`
- Modify: `internal/databases/rotation.go` (Keep struct), `internal/databases/schedule_store.go`, `internal/databases/backup_store.go` (Upload), `internal/backupdest/config.go` (Destination), `internal/backupdest/store.go`
- Test: `internal/databases/schedule_store_test.go`, `internal/databases/backup_store_test.go`, `internal/backupdest/backupdest_test.go`, `internal/database/*_test.go` (version pins 51→52)

**Interfaces:**
- Produces:
  - `const RotateSimple = "simple"`, `const RotateGFS = "gfs"` in `rotation.go`.
  - `type Keep struct { Mode string; Count, Days *int; GFSDays, GFSWeeks, GFSMonths, GFSYears *int }` — `Mode` `""` reads as Simple everywhere.
  - `Upload.Encrypted bool`, persisted; `InsertUpload` stores it.
  - `Destination.Encrypt bool`, `Destination.Passphrase string`; `store.Create`/`store.Update` persist them. On `Update`, an empty `Passphrase` with `Encrypt` true keeps the stored one (same pattern as `Secret`); `Encrypt` false always stores `''`.

- [ ] **Step 1: Write the migration**

```sql
-- +goose Up
-- Phase 9 (.scratch/databases/plan-phase-9.md, ADR-0046): GFS Rotation modes
-- per set, and encrypted copies. passphrase is a secret like secret: the API
-- never returns it outside the audited reveal.
ALTER TABLE database_backup_schedules ADD COLUMN local_mode TEXT NOT NULL DEFAULT 'simple';
ALTER TABLE database_backup_schedules ADD COLUMN local_gfs_days INTEGER;
ALTER TABLE database_backup_schedules ADD COLUMN local_gfs_weeks INTEGER;
ALTER TABLE database_backup_schedules ADD COLUMN local_gfs_months INTEGER;
ALTER TABLE database_backup_schedules ADD COLUMN local_gfs_years INTEGER;
ALTER TABLE database_backup_schedules ADD COLUMN remote_mode TEXT NOT NULL DEFAULT 'simple';
ALTER TABLE database_backup_schedules ADD COLUMN remote_gfs_days INTEGER;
ALTER TABLE database_backup_schedules ADD COLUMN remote_gfs_weeks INTEGER;
ALTER TABLE database_backup_schedules ADD COLUMN remote_gfs_months INTEGER;
ALTER TABLE database_backup_schedules ADD COLUMN remote_gfs_years INTEGER;
ALTER TABLE backup_destinations ADD COLUMN encrypt INTEGER NOT NULL DEFAULT 0;
ALTER TABLE backup_destinations ADD COLUMN passphrase TEXT NOT NULL DEFAULT '';
ALTER TABLE database_backup_uploads ADD COLUMN encrypted INTEGER NOT NULL DEFAULT 0;
```

Down drops the columns in reverse order.

- [ ] **Step 2: Write the failing store tests**

In `schedule_store_test.go`, extend the existing save/get round-trip (or add `TestScheduleGFSRoundTrip`): save a Schedule with `Local: Keep{Mode: RotateGFS, Count: new(3), GFSDays: new(7), GFSWeeks: new(4), GFSMonths: new(12), GFSYears: new(2)}` and `Remote: Keep{Mode: RotateGFS, GFSDays: new(14)}`, read it back, `reflect.DeepEqual` on both Keeps; a Database with no row still reads `DefaultSchedule` with `Mode == ""`.

In `backup_store_test.go`: `InsertUpload(ctx, Upload{..., Encrypted: true})` round-trips `Encrypted` through `LiveUploads` and `ListBackups`.

In `backupdest_test.go`: `TestEncryptPassphraseRoundTrip` — Create with `Encrypt: true, Passphrase: "p-0123456789-0123456789"`; Get returns both; Update with `Passphrase: ""` keeps the stored one; Update with `Encrypt: false` clears it to `""` (assert via Get).

- [ ] **Step 3: Run the tests to verify they fail** (`go test ./internal/databases/ ./internal/backupdest/ -run 'GFS|Encrypt|Upload'`)
- [ ] **Step 4: Implement** — extend `Keep`, `scheduleColumns` (append the ten new columns), `scanSchedule`, `SaveSchedule` upsert; `Upload.Encrypted` + its columns list and scans; `Destination` fields + `backupdest` store Create/Update/scan. Bump the schema-version pins in `internal/database/*_test.go` to 52.
- [ ] **Step 5: Run the package tests and the whole `go test ./...`** — PASS. Run the migration Down→Up once on a scratch DB (goose on a copy, as in phase 8).
- [ ] **Step 6: Commit** `feat: store gfs rotation modes and encrypted-copy settings`

### Task 2: GFS in `Rotate`

**Files:**
- Modify: `internal/databases/rotation.go`
- Test: `internal/databases/rotation_test.go`

**Interfaces:**
- Consumes: `Keep` from Task 1. `Rotate(items []RotItem, k Keep, now time.Time) []int64` keeps its signature; callers change nowhere.
- Produces: GFS semantics — an item is **kept** when any rule keeps it: (a) the newest OK item of its group (existing rule, both modes); (b) its index in the newest-first order is `< *k.Count` (nil Count keeps nothing by count; failed items occupy count slots exactly as in Simple); (c) it is the newest **OK** item of a calendar bucket within the last D days / W ISO weeks / M months / Y years, buckets taken in `now.Location()`. `k.Days` is ignored in GFS mode (Task 3 normalizes it to nil). Failed items never fill a tier slot.

- [ ] **Step 1: Write the failing table test**

Add `TestRotateGFS` beside the Simple cases, with `now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)` and rows asserting exactly which IDs are deleted:
- *count*: `Keep{Mode: RotateGFS, Count: new(2)}` over 4 OK dailies keeps the 2 newest, deletes the rest.
- *daily tier*: `GFSDays: new(3)` over two backups on each of the last 5 days keeps the newest of each of the last 3 days (per group).
- *midnight boundary (Review Focus 1)*: items at Nov 16 23:59 and Nov 17 00:01 with `now` Nov 17 00:05 and `GFSDays: new(1)` — only the 00:01 item is in today's bucket; with `GFSDays: new(2)` both days' newest are kept.
- *weekly*: `GFSWeeks: new(2)` with items on Mon Nov 10 (ISO week 46) and Mon Nov 17 (week 47) keeps both newest; an item in week 45 goes.
- *monthly / yearly*: `GFSMonths: new(2)` keeps newest of Nov and Oct, deletes September's; `GFSYears: new(1)` keeps only this year's newest per bucket.
- *failed never fills a slot*: a day whose only items are failed keeps nothing for that day (but the group's newest OK overall is still kept); a failed item newer than an OK one in the same day does not displace it.
- *any rule keeps*: an item kept by the weekly tier but past Count stays.
- *timezone*: same instants, `now` in `time.FixedZone("ICT", 7*3600)` flips which items share a day — one row proving buckets follow `now.Location()` (convert item times with `.In` inside the expectation reasoning, not in the code under test).
- *Simple untouched*: one row with `Mode: ""` and one with `Mode: RotateSimple` asserting today's behavior byte-for-byte against an existing case.

- [ ] **Step 2: Run to verify the GFS rows fail**
- [ ] **Step 3: Implement GFS in `Rotate`** — keep it one pure function; suggested helpers in the same file: `dayDiff(now, at time.Time) int` (calendar days in `now.Location()`), `weekDiff` (via each time's ISO-week Monday date, then `dayDiff/7`), `monthDiff`, `yearDiff`.
- [ ] **Step 4: Run `go test ./internal/databases/ -run TestRotate`** — PASS
- [ ] **Step 5: Commit** `feat: add gfs tiers to backup rotation`

### Task 3: Schedule validation and JSON

**Files:**
- Modify: `internal/databases/schedule_http.go`
- Test: `internal/databases/schedule_http_test.go`

**Interfaces:**
- Consumes: `Keep` with Mode/GFS fields.
- Produces: `ScheduleJSON` gains `LocalMode string \`json:"localMode"\``, `RemoteMode string \`json:"remoteMode"\``, and `*int` fields `LocalGfsDays/LocalGfsWeeks/LocalGfsMonths/LocalGfsYears` and `RemoteGfs…` (JSON `localGfsDays`…). `scheduleReply` fills them; `putSchedule` maps them into the two Keeps. `SaveSchedule` (service) normalizes and validates per set:
  - Mode `""` becomes `RotateSimple`; anything other than `simple`/`gfs` → `ErrInvalidSchedule{"rotation mode must be simple or gfs"}`.
  - Every set value (Count, Days, the four GFS fields) ≥ 1 when present (extend the existing loop).
  - Simple: the four GFS fields are cleared to nil. GFS: `Days` is cleared to nil, and at least one of Count/GFSDays/GFSWeeks/GFSMonths/GFSYears must be set, else `ErrInvalidSchedule{"keep at least one tier"}` (Review Focus 2).

- [ ] **Step 1: Write the failing tests** — in the existing schedule HTTP test style: PUT a GFS local set and assert GET echoes mode and tiers, and that a stored Simple schedule answers `localMode: "simple"`; PUT mode `gfs` with no tiers → 422 containing `keep at least one tier`; PUT mode `gfs` with `localKeepDays` set → stored `localKeepDays` is null (cleared); PUT mode `x` → 422.
- [ ] **Step 2: Run to verify they fail**
- [ ] **Step 3: Implement** in `scheduleReply`, `putSchedule` and `Service.SaveSchedule`.
- [ ] **Step 4: Run `go test ./internal/databases/ -run Schedule`** — PASS
- [ ] **Step 5: Commit** `feat: accept gfs rotation in the schedule api`

### Task 4: `internal/agefile`

**Files:**
- Create: `internal/agefile/agefile.go`
- Test: `internal/agefile/agefile_test.go`
- Modify: `go.mod` (add `filippo.io/age` v1.2.1)

**Interfaces:**
- Produces:
  - `const WorkFactor = 15`
  - `func IsAge(prefix []byte) bool` — true when prefix begins `age-encryption.org/v1\n` (binary header only; armored files are not detected and fall through to the SQL check).
  - `func Encrypt(dst io.Writer, passphrase string, src io.Reader) error` — scrypt recipient at `WorkFactor`.
  - `func Decrypt(passphrase string, src io.Reader) (io.Reader, error)` — scrypt identity with max work factor `WorkFactor`.
  - `var ErrWrongPassphrase = errors.New("wrong passphrase, or the file is not encrypted with a passphrase")`
  - `var ErrTooExpensive = errors.New("this file's scrypt work factor is above 2^15, which the panel cannot afford: decrypt it with `+"`age -d`"+` first and upload the result")`
  - A package-level `sync.Mutex` held across every key derivation (the `age.Encrypt` and `age.Decrypt` calls), so one derivation runs at a time in the whole panel.

- [ ] **Step 1: Write the failing tests**

```go
func TestRoundTripAndHeader(t *testing.T)   // Encrypt "hello" → buf starts with the magic, IsAge true; Decrypt with the right passphrase reads "hello"; IsAge(gzip magic) false
func TestWrongPassphrase(t *testing.T)      // Decrypt with another passphrase → ErrWrongPassphrase (map age's "no identity matched" to it)
func TestDerivationMemory(t *testing.T)     // runtime.ReadMemStats around one Decrypt derivation: TotalAlloc delta ≥ 16 MiB (the measurement works) and < 128 MiB (2^15, never 2^18's 256 MiB)
func TestTooExpensiveRefusedWithoutDeriving(t *testing.T)
func TestOneDerivationAtATime(t *testing.T)
```

`TestTooExpensiveRefusedWithoutDeriving`: encrypt normally, then tamper the header text — replace the scrypt stanza's ` 15` with ` 22` (first occurrence in the bytes before the first empty payload line) — and assert `Decrypt` returns `ErrTooExpensive` with a TotalAlloc delta < 8 MiB (Review Focus 5). `TestOneDerivationAtATime`: lock the package mutex directly from the test (same package), start `Encrypt` in a goroutine, assert it has not finished after 100 ms, unlock, assert it finishes.

- [ ] **Step 2: `go get filippo.io/age@v1.2.1`, run the tests to verify they fail**
- [ ] **Step 3: Implement.** Map errors by inspecting what `age.Decrypt` returns for each case (a `NoIdentityMatchError`-style error for a wrong passphrase; the work-factor refusal comes from `ScryptIdentity` with `SetMaxWorkFactor(WorkFactor)` before any derivation — verify empirically in the test and match that error, not a guessed string).
- [ ] **Step 4: Run `go test ./internal/agefile/`** — PASS, and `go mod tidy` leaves the tree clean.
- [ ] **Step 5: Commit** `feat: add the age passphrase file helper`

### Task 5: Destination encryption API

**Files:**
- Modify: `internal/backupdest/http.go`, `internal/authz/routescan_test.go` (route list)
- Test: `internal/backupdest/backupdest_test.go`

**Interfaces:**
- Consumes: `Destination.Encrypt/Passphrase` from Task 1.
- Produces:
  - `DestinationJSON.Encrypt bool \`json:"encrypt"\``; `input` gains `Encrypt bool \`json:"encrypt"\`` and `Passphrase string \`json:"passphrase"\``.
  - `readInput` rules: with `Encrypt` true on create, a trimmed passphrase of ≥ 20 runes is required (`FieldError{"passphrase", "the passphrase must be at least 20 characters"}`); on update, empty keeps the stored one (the store already does), non-empty must be ≥ 20; `Encrypt` false ignores the field.
  - `GET /api/backup-destinations/{id}/passphrase` → `{"passphrase": "…"}`; 422 `this destination does not encrypt copies` when `Encrypt` is false; `audit.Target(d.Name)` + `audit.Emit(ctx, "secret.read", map[string]any{"keys": []string{"passphrase"}})`. Registered in `RegisterRoutes` and added to the admin route list in `routescan_test.go`.
  - The `test` handler scrubs `d.Passphrase` wherever it scrubs `d.Secret`.

- [ ] **Step 1: Write the failing tests** — create with `encrypt` and a 23-char passphrase → 201 whose body does **not** contain the passphrase but has `"encrypt":true`; create with 10 chars → 422 field `passphrase`; reveal → 200 with the passphrase; reveal on a plain destination → 422; update turning `encrypt` off then reveal → 422 (forgotten).
- [ ] **Step 2: Run to verify they fail**
- [ ] **Step 3: Implement**
- [ ] **Step 4: Run `go test ./internal/backupdest/ ./internal/authz/`** — PASS
- [ ] **Step 5: Commit** `feat: let a backup destination encrypt its copies with a passphrase`

### Task 6: Encrypt then upload

**Files:**
- Modify: `internal/databases/uploads.go`, `internal/databases/backup_http.go` (`uploadJSON`)
- Test: `internal/databases/remote_rotation_test.go` or `internal/databases/uploads_test.go` (follow where `Service.upload` is tested today)

**Interfaces:**
- Consumes: `agefile.Encrypt`; `dest.Encrypt`, `dest.Passphrase`; `Upload.Encrypted`.
- Produces: in `Service.upload`, when `dest.Encrypt`:
  - the object key gets `".age"` appended (so the copy is `<file>.age`),
  - the Backup's file is encrypted into a temp file in the same `backups` dir (`os.CreateTemp(dir, base+"-*.age.tmp")`, mode 0600), uploaded via the existing `s.put` (exact size of the **encrypted** file), and the temp file removed in a defer,
  - an encryption failure records `UploadFailed` with the error (scrubbed of the passphrase) and removes the temp,
  - the upload row carries `Encrypted: true`.
  - `uploadJSON` gains `Encrypted bool \`json:"encrypted"\``.
  - The destination's settings are read once at upload start; Retry uses the current settings (a retried copy is a new copy).

- [ ] **Step 1: Write the failing tests** — with the existing fake destination/remote rig: (a) encrypt on → the remote's recorded key ends `.age`, the recorded `size` equals the byte length the fake received, the received bytes start with `agefile` magic and `agefile.Decrypt` with the destination's passphrase yields the original dump bytes, the upload row has `Encrypted` true, and no `*.age.tmp` remains in the backups dir (Review Focus 3); (b) a remote `Put` failure still removes the temp and records `UploadFailed`; (c) encrypt off → key unchanged, `Encrypted` false.
- [ ] **Step 2: Run to verify they fail**
- [ ] **Step 3: Implement**
- [ ] **Step 4: Run `go test ./internal/databases/`** — PASS
- [ ] **Step 5: Commit** `feat: encrypt copies to an encrypting destination`

### Task 7: Restore from an age file

**Files:**
- Modify: `internal/databases/restore.go`, `internal/databases/backup_http.go` (`uploadRestore`), `internal/databases/http.go` (`writeError` mapping if that is where restore errors map)
- Test: `internal/databases/restore_test.go`, `internal/databases/backup_http_test.go`

**Interfaces:**
- Consumes: `agefile.IsAge/Decrypt/ErrWrongPassphrase/ErrTooExpensive`.
- Produces:
  - `RestoreFromUpload(ctx context.Context, id int64, target, confirm, name, passphrase string, body io.Reader) (Restore, error)` — after the body is on disk, peek its first 22 bytes; when `IsAge`: an empty passphrase returns `ErrNeedPassphrase = errors.New("this file is encrypted: enter its passphrase")`; otherwise decrypt (serialized derivation) streaming into a second temp file in the same `uploads` dir, remove the encrypted temp, and continue with the decrypted path through `checkUploadFile` and `startRestore` unchanged. Every error path removes both temp files.
  - `uploadRestore` reads the passphrase from the `X-Passphrase` request header (never the query, which can end up in logs).
  - `ErrNeedPassphrase`, `agefile.ErrWrongPassphrase`, `agefile.ErrTooExpensive` map to 422.

- [ ] **Step 1: Write the failing tests** — service level with a real SQLite rig and a fake dumper as the existing restore tests do: (a) an `agefile`-encrypted `.sql` restores with the right passphrase (the dumper sees the plain SQL); (b) wrong passphrase → error wrapping `ErrWrongPassphrase` and `uploads/` is empty after (Review Focus 4); (c) age file + empty passphrase → `ErrNeedPassphrase`; (d) a header tampered to work factor 22 → `ErrTooExpensive`; (e) HTTP: `X-Passphrase` reaches the service and a 422 body carries the message.
- [ ] **Step 2: Run to verify they fail**
- [ ] **Step 3: Implement**
- [ ] **Step 4: Run `go test ./internal/databases/`** — PASS
- [ ] **Step 5: Commit** `feat: restore an age-encrypted upload`

### Task 8: GFS in the schedule card

**Files:**
- Modify: `web/src/components/DatabaseScheduleCard.tsx`, `web/src/data/types.ts`
- Test: `web/src/components/DatabaseScheduleCard.test.tsx`

**Interfaces:**
- Consumes: the schedule JSON fields of Task 3 (`localMode`, `localGfsDays`… `remoteMode`, `remoteGfs…`).
- Produces: per Rotation set a `Radio.Group` (options `Simple` / `GFS`, aria-labels "Rotation" and "Destination: rotation"). Simple shows today's two fields; GFS shows five `KeepField`-style `InputNumber`s (blank = keep nothing for that tier) labeled `Last N backups`, `Daily for D days`, `Weekly for W weeks` (help text mentions ISO weeks), `Monthly for M months`, `Yearly for Y years` (Destination variants prefixed `Destination: `). The PUT payload always carries both modes and all tier values (null when blank); switching back to Simple sends the GFS fields as null.

- [ ] **Step 1: Read the antd docs per Global Constraints.**
- [ ] **Step 2: Write the failing tests** — render with a GFS schedule → the radio shows GFS and `Daily for D days` has its value; switch to Simple → tier inputs disappear, save payload has `localMode: 'simple'` and null tiers; switch to GFS, fill Daily=7, save → payload `localMode: 'gfs'`, `localGfsDays: 7`, `localKeepDays: null`.
- [ ] **Step 3: Run to verify they fail, implement, run `pnpm vitest run src/components/DatabaseScheduleCard.test.tsx`** — PASS
- [ ] **Step 4: Commit** `feat: pick gfs rotation in the schedule card`

### Task 9: Encryption in the UI

**Files:**
- Modify: `web/src/pages/BackupDestinationsPage.tsx`, `web/src/pages/DatabaseBackupsTab.tsx`, `web/src/data/types.ts`, `web/src/data/api.ts`
- Test: matching `.test.tsx` files

**Interfaces:**
- Consumes: `encrypt` on the destination JSON, the passphrase reveal endpoint, `uploadJSON.encrypted`, the `X-Passphrase` header of Task 7.
- Produces:
  - `api.ts`: `revealDestinationPassphrase(id): Promise<{passphrase: string}>`; the restore-upload call takes an optional passphrase and sends it as the `X-Passphrase` header; destination input type gains `encrypt: boolean; passphrase: string`.
  - Destination form: Checkbox **Encrypt copies with a passphrase**; when ticked, an `Input.Password` with a **Generate** button filling 24 chars from `crypto.getRandomValues` over a base58 alphabet, and a `Text strong` warning: *"The panel cannot help if this server is lost: keep a copy of the passphrase somewhere else, or every encrypted copy is unreadable."* On edit, the placeholder says an empty field keeps the stored passphrase; submitting with the box unticked after it was on, or with a new passphrase, first shows `Modal.confirm` — *"Existing copies keep the old passphrase and the panel forgets it. Make sure you have it somewhere else."* A **Reveal passphrase** action on an encrypting destination calls the reveal endpoint and shows the value in a copyable `Text`.
  - Destinations table: a `Tag` **Encrypted** on encrypting rows.
  - Backups tab: when `row.upload?.encrypted`, the copy's tooltip/label carries `Encrypted (.age)`.
  - Restore-from-file dialog: after a file is chosen, read its first 22 bytes (`file.slice(0, 22).text()`); when they are the age header, show a required Passphrase `Input.Password` and send it with the upload.

- [ ] **Step 1: Read the antd docs per Global Constraints.**
- [ ] **Step 2: Write the failing tests** — destinations page: creating with encrypt ticked and 10 chars shows the server's field error; the reveal action calls the API and renders the value; unticking on edit opens the confirm with the forget copy. Backups tab: an encrypted copy shows `Encrypted (.age)`; choosing a file whose bytes start with the age header shows the Passphrase field and the api call carries it.
- [ ] **Step 3: Run to verify they fail, implement, run the two test files** — PASS
- [ ] **Step 4: Run `pnpm exec tsc -b --noEmit`, `pnpm vitest run`, `pnpm build`** — PASS
- [ ] **Step 5: Commit** `feat: encrypt backup copies from the ui`

### Task 10: e2e, ADR, CONTEXT

**Files:**
- Modify: `e2e/tests/backup-destinations.spec.ts`, `e2e/package.json` (+ lockfile)
- Create: `docs/adr/0046-gfs-rotation-and-encrypted-copies.md`
- Modify: `CONTEXT.md`

**Interfaces:**
- Consumes: everything above, the existing MinIO + `mc` rig in `backup-destinations.spec.ts`.

- [ ] **Step 1: Add `age-encryption` (FiloSottile's typage; pin the latest version ≥ 2 weeks old) to `e2e/package.json` devDependencies and install.**
- [ ] **Step 2: Write the e2e test** `an encrypted copy opens with age and restores through the panel` following the existing scheduled-copy test: create the MinIO destination with encryption on (keep the generated passphrase in the test), schedule a Postgres Database to copy to it, wait for the uploaded copy, assert its API row has `encrypted: true` and its key ends `.age`; `mc cat` the object through the mc container piped to `base64`, decode in Node and decrypt with `age-encryption` using the passphrase — the plaintext contains the seeded row's value. Then restore that `.age` file through the panel's Restore-from-file UI with the passphrase and assert the data is back; and upload a typage-encrypted file (typage's default work factor 2^18 > 2^15) and assert the refusal message tells the user to `age -d` first. Never print the passphrase in assertions or logs.
- [ ] **Step 3: Run the spec in the sandbox** (`task e2e -- backup-destinations`), then the full `task e2e`. As in phase 8, if this branch's sandbox scripts lag main, run on a scratch branch merged with `origin/main` and cherry-pick fixes back.
- [ ] **Step 4: Write ADR-0046** — decision (GFS as a mode on the pure `Rotate`; any-rule-keeps; failed backups never fill a slot; buckets in the host tz; age scrypt 2^15 because of the 256 MiB container, one derivation at a time; encrypt-then-upload with a temp file; passphrase stored, reveal audited, forgotten when turned off; `.age` restore detected by header, capped work factor), considered options (per-tier cron expressions; encrypting locally too — rejected: local restores must not need a passphrase; X25519 recipients — rejected: nothing safe to store the identity in), consequences (copies need `age -d` outside the panel; a lost passphrase loses the copies; typage interop proven in e2e; retried copies use current settings). Add **GFS Rotation** and **Encrypted copy** to CONTEXT.md's terms.
- [ ] **Step 5: Commit** `test: prove gfs rotation and encrypted copies end to end` (ADR + CONTEXT may ride along or go as `docs:`).

---

## Decisions beyond the spec (flag any you disagree with)

1. **GFS reuses `keep_count` as its "last N"**; "at most X days" is Simple-only and cleared on a GFS save (one meaning per column).
2. **Tier buckets are computed in `now.Location()`** — the scheduler already passes `s.d.Now()` in the panel/host timezone.
3. **The passphrase is its own column**, never part of the config JSON, and turning encryption off clears it server-side ("the panel forgets it").
4. **Passphrase generation is client-side** (`crypto.getRandomValues`, 24 base58 chars ≈ 140 bits); the server only enforces ≥ 20 runes.
5. **The restore passphrase travels in an `X-Passphrase` header**, not the query string, so it cannot land in access logs.
6. **e2e proves interop with typage (`age-encryption` npm), not the `age` CLI** — the CLI is not on the e2e host; typage is the same author's reference TypeScript implementation. The 2^22-without-memory-spike check is the Go unit test with a tampered header; the e2e refusal uses typage's default 2^18 file.
7. **Retrying a failed copy encrypts with the destination's current settings** — a retried copy is a new copy, per "applies to new copies only".
8. **Armored age files are not detected** (binary header only); they fall through to the SQL check and are refused as not-SQL.
9. **GFS mode with no count and no tiers is refused** ("keep at least one tier") instead of silently keeping only the newest success.
10. **An age file encrypted to X25519 recipients** (no passphrase stanza) surfaces as `ErrWrongPassphrase`'s "or the file is not encrypted with a passphrase".

## Self-review notes

- Spec coverage: every 9a line maps to Tasks 1–3, 8; every 9b line to Tasks 1, 4–7, 9; the Testing-section phase-9 line to Tasks 2, 4, 10.
- `Rotate`'s signature is unchanged, so `rotateLocal` and `rotateRemote` need no edits beyond what Task 1's `Keep` carries — verified against `scheduler.go:220` and `uploads.go:250`.
- The exact age error values for Task 4's mapping are deliberately left to the implementer to establish empirically in the RED step, because guessing library error strings in a plan bakes in mistakes.
