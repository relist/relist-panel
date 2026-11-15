# Databases phase 6a (SFTP Backup destination) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An Admin adds an SFTP server (Hetzner Storage Box, Synology, any OpenSSH) as a Backup destination, authenticated with a key from the SSH keys library or a password. Scheduled Backups are copied to it exactly as they are to S3. The server's host key is trusted on the first successful Test and checked on every later connection.

**Architecture:**
- **`internal/backupdest`** gains a second `type`, `sftp`.
  - **Store.** Two new columns: `ssh_key_id`, a real foreign key to `ssh_keys`, and `host_key`, the trusted key, kept out of `config` so recording it never looks like a settings change.
  - **Client.** `sftpRemote` dials lazily: every `Put`, `Delete` and `Test` opens one SSH connection and closes it. `Destinations.Open` is also called just to read a name (`destinationName`, schedule save), and that must never log in.
  - **Timeouts.** Neither `pkg/sftp` nor `x/crypto/ssh` honours `ctx`, so closing the `net.Conn` when the context ends is what enforces the 5-minute stall cut and the 1-minute Delete and Test timeouts.
- **`internal/databases`** reads the key prefix through a type-neutral `Destination.KeyPrefix()`. A schedule can only pick an SFTP destination after it has passed a Test.
- **`internal/sshkeys`** refuses to delete a key that a destination uses. Sources keep today's behaviour, where the Source loses its key (see the note in Global Constraints).
- **UI:**
  - The Backup destinations modal gets a Type choice, the SFTP fields, the host key, and **Reset host key**.
  - The SSH keys page lists the destinations that use each key.
- **Delivery:** one PR, branch `claude/databases-phase-6a` on `claude/databases-phase-5` (#87).

**Tech Stack:** Go, `github.com/pkg/sftp` v1.13.12 on `golang.org/x/crypto/ssh` (already v0.58.0), SQLite/goose, React 19, antd 6.5.1, vitest, Playwright.

**Spec:** `.scratch/databases/spec-2.md`, "Phase 6" (the shared rules) and "6a: SFTP". Every S3 rule from ADR-0040 holds unless this plan says otherwise.

## Global Constraints

- **Type.** `type: "sftp"`. A destination's type cannot change on edit: 422 on field `type`, "the type of a Backup destination cannot change".
- **Settings** (`SFTPConfig`, JSON in `config`):
  - `host`: trimmed and required; IPv6 is allowed; it may not contain `/` or whitespace.
  - `port`: 0 becomes 22; otherwise 1–65535.
  - `user`: required; no whitespace or control characters.
  - `path`: the base path (see Path rule).
  - `auth`: `"key"` or `"password"`.
- **Path rule** (`ValidateSFTP`):
  - Trim spaces, then `path.Clean`. `""` and `"."` become `""`, the login directory.
  - `"/"` is refused: "base path cannot be /". Any `..` element is refused: "base path cannot contain ..".
  - A leading `/` is kept: `/backups` is absolute, `backups` is relative to the login directory.
- **Columns** (migration `internal/migrations/00049_sftp_destinations.sql`, the only new file; never edit 00047 or 00048):
  - `ssh_key_id INTEGER REFERENCES ssh_keys(id) ON DELETE RESTRICT`
  - `host_key TEXT NOT NULL DEFAULT ''`
  - If SQLite refuses `DROP COLUMN` on a column with `REFERENCES`, Down rebuilds the table. Run Down once by hand to check it.
- **Secret.** `secret` holds the SFTP password for `auth: "password"` and `""` for `auth: "key"`.
  - Neither the password nor the private key ever appears in a response, log, error or audit detail. Errors pass through `scrub`.
  - On edit, an empty secret keeps the stored password. The exceptions answer 422 on field `secret`, "enter the password again":
    - the host or the port changed;
    - the auth switched from key to password.
  - Switching to key auth stores `""`.
- **Host key.**
  - Stored as one `authorized_keys` line, `ssh.MarshalAuthorizedKey` without the newline.
  - Shown to the browser as `<type> <ssh.FingerprintSHA256>`, for example `ssh-ed25519 SHA256:abc…`, or `""` when none is recorded.
  - Compared by marshalled bytes, never by fingerprint string.
  - Only a successful Test records it. Changing the host or the port forgets it. **Reset host key** forgets it and the last Test result.
- **Error texts** (exact):
  - `ErrHostKeyChanged`: "the server's host key changed; if you expected this, use Reset host key and Test again".
  - `ErrNoHostKey`: "this SFTP destination has no trusted host key yet: run Test first".
  - Schedule save: `ErrInvalidSchedule{"test the Backup destination first: it has no trusted host key"}`.
- **Object key.** It is `backupdest.ObjectKey(dest.KeyPrefix(), …)`, the same layout as S3. `KeyPrefix()` is `S3.Prefix` for S3 and `SFTP.Path` for SFTP. The recorded `object_key` therefore carries the base path, and a later change of path never redirects deletes.
- **Upload.** Write `<key>.part`, check its size with `Stat`, then rename it to `<key>`.
  - The rename uses `posix-rename@openssh.com` when the server offers it; otherwise remove `<key>` (a missing file is fine) and then `Rename`.
  - A failed upload removes `.part`, over the same connection if it still works, otherwise over a fresh one with a 1-minute timeout.
  - Missing directories are made with `MkdirAll`.
- **Timeouts.** Unchanged from phase 3: a 5-minute stall cut in `databases.put`, and `remoteTimeout` (1 minute) on Delete and Test. Dial with `net.Dialer{Timeout: 30 * time.Second}`. Use `context.AfterFunc(ctx, func() { conn.Close() })` for everything after the dial.
- **Audit.**
  - New route `POST /api/backup-destinations/{id}/reset-host-key` → action `backup_destination.reset_host_key`, added in `internal/authz/actions.go` beside `…/test`. The target is the destination name.
  - A Test that records a host key adds `audit.Detail(ctx, "hostKey", "<type> SHA256:…")`.
- **SSH keys.**
  - A key used by any destination cannot be deleted: 409 `{"error":"used by Backup destinations: a, b","destinations":["a","b"]}`.
  - Note for the user: the spec says "as with Compose sources", but Sources are not refused today. Deleting their key sets it to NULL (`sshkeys.Store.Delete`, `DELETE_IN_USE_WARNING`). That behaviour stays as it is.
- **Dependency.** Only `github.com/pkg/sftp v1.13.12` is added; its indirect `github.com/kr/fs` comes with it. If `go get` raises an existing module (such as `x/crypto`), that is fine; say so in the report.
- **Who.** Admin only. Nothing is added to authz `Routes`.
- **e2e server.**
  - Image `lscr.io/linuxserver/openssh-server:10.3_p1-r2-ls243`, verified pullable in the sandbox on 2026-11-15.
  - Port 2222, home `/config`, `internal-sftp`, and ed25519, ECDSA and RSA host keys, all generated again when the container is recreated.
  - Environment variables: `USER_NAME`, `PUBLIC_KEY`, `PASSWORD_ACCESS=true` and `USER_PASSWORD`.
- **Frontend.** Read `https://ant.design/llms.txt` and the antd pages you use (antd 6.5.1). Follow `docs/GUIDELINES.md`.
- **Commits.** Conventional Commits, no scope, with the repo's `Co-Authored-By` trailer.
- **Go tests.** Run as `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./...`.
- **Docker.** Only through the sandbox `DOCKER_HOST` in `.data/e2e/env`.

## Review Focus

1. **A server that accepts TCP and never answers.** The SSH handshake hangs. Test must return within its timeout, not hang the request. Test in Task 2: `TestSFTPHungServerTimesOut`.
2. **A server that offers several host key types.** OpenSSH offers ed25519, ECDSA and RSA. Once a key is recorded, later connections must ask for that key's algorithm, or the server may present another type and every upload fails with a false "host key changed". Test in Task 2: `TestSFTPRecordedAlgorithmIsAsked`.
3. **A tested destination is moved to another host.** After the host or port changes, the old key is forgotten, uploads say "run Test first" rather than "host key changed", and a schedule can still be saved once Test passes. Test in Task 3: `TestChangingHostForgetsHostKey`.
4. **An interrupted upload.** The reader ends early or the connection is cut. No file of the wrong size may sit under the final name, and no `.part` is left. Test in Task 2: `TestSFTPPutFailureLeavesNothing`.
5. **Deleting the SSH key a destination uses.** Delete is refused with 409 naming the destination, the key still exists, and the destination still connects. Test in Task 4: `TestDeleteKeyUsedByDestination`.

---

### Task 1: Migration, SFTP settings and store

**Files:**
- Create: `internal/migrations/00049_sftp_destinations.sql`
- Modify:
  - `internal/backupdest/config.go`: types, `ValidateSFTP`, `KeyPrefix`, errors;
  - `internal/backupdest/store.go`: the join and the new columns;
  - the schema-version tests that pin 48 now pin 49 (find them with `grep -rn "48" --include=*_test.go internal | grep -i version`).
- Test: `internal/backupdest/backupdest_test.go`, `internal/backupdest/store_sftp_test.go`

**Interfaces:**
- Produces:
  ```go
  const TypeSFTP = "sftp"
  const AuthKey, AuthPassword = "key", "password"
  type SFTPConfig struct {
      Host string `json:"host"`; Port int `json:"port"`; User string `json:"user"`
      Path string `json:"path"`; Auth string `json:"auth"`
  }
  // Destination gains:
  //   SFTP SFTPConfig
  //   SSHKeyID *int64
  //   SSHKeyName, PrivateKey, PublicKey string  // joined from ssh_keys; never saved here
  //   HostKey string                            // authorized_keys line or ""
  func (d Destination) KeyPrefix() string
  func ValidateSFTP(c SFTPConfig) (SFTPConfig, error) // *FieldError on host/port/user/path/auth
  func (s *Store) SetHostKey(ctx context.Context, id int64, line string) error   // only when host_key = ''
  func (s *Store) ResetHostKey(ctx context.Context, id int64) error              // clears host_key and last test
  var ErrHostKeyChanged, ErrNoHostKey error
  ```
  `scan` unmarshals `config` into `S3` or `SFTP` by `type`. Every SELECT reads `FROM backup_destinations d LEFT JOIN ssh_keys k ON k.id = d.ssh_key_id`. `Create` and `Update` write `ssh_key_id`.

- [ ] **Step 1: Write the failing tests**
  - `TestValidateSFTP` (table):
    - `{Host:" box.example "}` gives host `box.example` and port 22;
    - `Path:"backups/"` gives `backups`; `Path:"/srv//b/"` gives `/srv/b`; `Path:"."` gives `""`;
    - these are refused with the field named: `Path:"/"`, `Path:"a/../../x"`, `Host:""`, `Host:"a b"`, `Port:70000`, `User:""`, `Auth:"cert"`.
  - `TestKeyPrefix`: S3 `{Prefix:"p"}` gives `p`; SFTP `{Path:"/srv/b"}` gives `/srv/b`. `ObjectKey("/srv/b","app",3,"main","x.sql.gz") == "/srv/b/app-3/main/x.sql.gz"`.
  - `TestStoreSFTPRoundTrip`:
    - insert an `ssh_keys` row, then `Create` an SFTP destination with `SSHKeyID`;
    - `Get` returns the same `SFTP`, plus `SSHKeyName`, `PrivateKey` and `PublicKey` from the key, and `HostKey == ""`.
  - `TestSetHostKeyOnlyOnce`: `SetHostKey(id,"A")` then `SetHostKey(id,"B")` leaves `HostKey == "A"`.
  - `TestUpdateHostChangeForgetsHostKey`: with a host key set, `Update` with another `Port` gives `HostKey == ""`. `Update` with only another `Path` keeps it.
  - `TestUpdateToKeyAuthClearsSecret`: from password `"pw-123"`, `Update` with `Auth: AuthKey` gives `Secret == ""`.
  - `TestResetHostKey`: clears `HostKey`, `LastTestAt`, `LastTestOK` and `LastTestError`.
  - `TestDeleteSSHKeyRestricted`: a raw `DELETE FROM ssh_keys` on a used key fails. This is the database backstop.

- [ ] **Step 2: Run them and see them fail**

  Run: `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/backupdest/ -run 'SFTP|KeyPrefix|HostKey|KeyAuth|SSHKey'`
  Expected: build failure (undefined `TypeSFTP`, `ValidateSFTP`, …).

- [ ] **Step 3: Write `00049_sftp_destinations.sql` and the code in `config.go` and `store.go`**

  The comment heading the migration names this plan and the ADR from Task 3 (ADR-0042). `Update`'s "changed" check becomes:

  ```go
  old.S3 != d.S3 || old.SFTP != d.SFTP || !sameID(old.SSHKeyID, d.SSHKeyID) || d.Secret != ""
  ```

  It forgets `host_key` when `Host` or `Port` differs, and it stores `""` as the secret when `d.SFTP.Auth == AuthKey`.

- [ ] **Step 4: Run the package tests and the schema-version tests**

  Run: `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/backupdest/ ./internal/migrations/ ./internal/database/`
  Expected: PASS.

- [ ] **Step 5: Commit**: `feat: store sftp backup destinations`

---

### Task 2: The SFTP client

**Files:**
- Create:
  - `internal/backupdest/sftp.go`
  - `internal/backupdest/sftp_test.go`
  - `internal/backupdest/sftpserver_test.go`: an in-process test server.
- Modify: `go.mod`, `go.sum` (`go get github.com/pkg/sftp@v1.13.12`)

**Interfaces:**
- Consumes: `Destination`, `SFTPConfig`, `ErrHostKeyChanged`, `ErrNoHostKey` and `scrub` (Task 1 and phase 3).
- Produces:
  ```go
  func NewSFTP(d Destination) (Remote, error) // no network; refuses a missing key/password
  // HostKeyRecorder is implemented by a Remote whose Test may meet a host key for the first time.
  type HostKeyRecorder interface{ SeenHostKey() string } // authorized_keys line, "" if none new
  func hostKeyAlgorithms(k ssh.PublicKey) []string
  func renameInto(c *sftp.Client, from, to string, posix bool) error
  ```

**The connection** (one per `Put`, `Delete` or `Test`):
1. `DialContext`, then `stop := context.AfterFunc(ctx, func(){ conn.Close() })`, then `ssh.NewClientConn`, then `sftp.NewClient(ssh.NewClient(…), sftp.UseConcurrentWrites(true))`. Close everything and call `stop()` at the end.
2. **Auth.**
   - Key: `ssh.PublicKeys(ssh.ParsePrivateKey(PrivateKey))`.
   - Password: `ssh.Password(secret)` and `ssh.KeyboardInteractive`, answering every question with the secret, because some appliances only offer keyboard-interactive.
3. **Host key.** `HostKeyCallback` compares the presented key's `Marshal()` with the recorded key's.
   - `Test` with no recorded key accepts the presented key and keeps it for `SeenHostKey`, only once Test fully succeeds.
   - `Put` and `Delete` with no recorded key return `ErrNoHostKey` before dialling.
   - When a key is recorded, `HostKeyAlgorithms = hostKeyAlgorithms(recorded)`. For `ssh-rsa` that is `rsa-sha2-512`, `rsa-sha2-256`, `ssh-rsa`; for any other type it is `[]string{k.Type()}`.
4. **Test.** `MkdirAll(base)` when the base path is not empty. Write `.relist-test-<16 hex>` under it, read it back, compare, and remove it; the removal is deferred so it runs whatever happens.

- [ ] **Step 1: Write the test server and the failing tests**

  **The test server.** `startSFTP(t, opts) (addr string, hostKeys []ssh.Signer, root string)`:
  - it serves `127.0.0.1:0`, with the host keys from `opts`, defaulting to one ed25519 key;
  - it accepts one public key and/or one password;
  - it answers the `sftp` subsystem with `sftp.NewServer(ch, sftp.WithServerWorkingDirectory(root))`;
  - it counts accepted connections.

  **The tests:**
  - `TestSFTPPutWithKey`:
    - Test once to record the key;
    - put 1 MiB of random bytes under `backups/app-3/main/x.sql.gz`, a relative base path;
    - the file under `root` equals the bytes, and no `*.part` exists anywhere under `root`.
  - `TestSFTPPutWithPassword`: the same with password auth.
  - `TestSFTPTestRecordsUnknownKey`:
    - with `HostKey ""`, `Test` succeeds and `SeenHostKey()` equals the server's key line;
    - with a wrong password, `Test` fails and `SeenHostKey() == ""`.
  - `TestSFTPHostKeyChanged`:
    - record key A, then serve key B;
    - `Test`, `Put` and `Delete` all fail with `errors.Is(err, ErrHostKeyChanged)`;
    - no file is written under `root`.
  - `TestSFTPPutWithoutHostKey`: `Put` returns `ErrNoHostKey`, and the server counted 0 connections.
  - `TestSFTPRecordedAlgorithmIsAsked`:
    - the server holds an ed25519 key and an RSA key; record the RSA line;
    - `Put` succeeds 3 times;
    - `hostKeyAlgorithms(rsaKey)` is `{"rsa-sha2-512","rsa-sha2-256","ssh-rsa"}`, and for ed25519 it is `{"ssh-ed25519"}`.
  - `TestSFTPHungServerTimesOut`:
    - a raw `net.Listener` that accepts and never writes;
    - `Test` with a 300 ms context returns an error in under 3 s.
  - `TestSFTPStalledAfterHandshake`:
    - the test server completes SSH and accepts the `sftp` subsystem request, then never answers SFTP init;
    - `Put` with a 300 ms context returns an error in under 3 s.
    - This proves the conn-close stays armed after the handshake, which the 5-minute stall cut depends on.
  - `TestSFTPPutFailureLeavesNothing`: `Put(ctx, key, strings.NewReader("short"), 1000)` fails, and neither `key` nor `key+".part"` exists.
  - `TestRenameIntoFallback`: with `posix=false`, `renameInto` replaces an existing target. The file then holds the new content and `from` is gone.
  - `TestSFTPDeleteMissingIsNotError`.
  - `TestSFTPErrorsNeverCarryPassword`: the server rejects the password, and the error text does not contain it.

- [ ] **Step 2: Run them and see them fail**

  Run: `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/backupdest/ -run 'SFTP|RenameInto'`
  Expected: build failure (undefined `NewSFTP`).

- [ ] **Step 3: Implement `sftp.go`**

  Detect posix-rename with `client.HasExtension("posix-rename@openssh.com")`. Missing files are `errors.Is(err, fs.ErrNotExist)`.

- [ ] **Step 4: Run the package with the race detector**

  Run: `env -u DOCKER_CONFIG -u DOCKER_HOST go test -race ./internal/backupdest/`
  Expected: PASS.

- [ ] **Step 5: Commit**: `feat: copy backups to sftp servers`

---

### Task 3: The HTTP side, Reset host key and ADR-0042

**Files:**
- Modify:
  - `internal/backupdest/http.go`
  - `internal/authz/actions.go` (one line)
  - `CONTEXT.md`, if its **Backup destination** entry says S3 only
- Create: `docs/adr/0042-sftp-backup-destinations.md`
- Test: `internal/backupdest/backupdest_test.go`

**Interfaces:**
- Consumes: Task 1's store, and Task 2's `NewSFTP` and `HostKeyRecorder`.
- Produces the JSON the browser sees:
  ```go
  type SFTPJSON struct {
      Host string `json:"host"`; Port int `json:"port"`; User string `json:"user"`; Path string `json:"path"`
      Auth string `json:"auth"`; SSHKeyID *int64 `json:"sshKeyId"`
      SSHKeyName string `json:"sshKeyName"`; PublicKey string `json:"publicKey"` // response only
      HostKey string `json:"hostKey"` // "<type> SHA256:…" or "", response only
  }
  // DestinationJSON: S3 *S3JSON `json:"s3,omitempty"`; SFTP *SFTPJSON `json:"sftp,omitempty"`
  // input: {name, type, s3?, sftp?: {host,port,user,path,auth,sshKeyId}, secret}
  ```
- **Route:** `POST /api/backup-destinations/{id}/reset-host-key` returns 200 with the destination.

**Behaviour:**
- `Service.connect` defaults to `func connect(d Destination) (Remote, error)`, which switches on `d.Type`.
- `readInput` validates by type.
- `auth: "key"` needs `sshKeyId` naming an existing key. Otherwise the answer is 422 on field `sshKeyId`, "choose an SSH key".
- `test`: after a successful `Remote.Test`, if the remote is a `HostKeyRecorder` with a non-empty `SeenHostKey()`, call `store.SetHostKey`, then `audit.Detail(ctx, "hostKey", …)`.

- [ ] **Step 1: Write the failing tests**

  Use the existing rig. Its fake remote gets a `seen string` field and a `SeenHostKey()` method.
  - `TestCreateSFTPWithKey`:
    - 201;
    - the body has `sftp.publicKey` equal to the key's public half, and `sftp.hostKey == ""`;
    - no `"s3"` key in the body.
  - `TestSFTPPasswordRules`:
    - create with password auth and no secret: 422 on `secret`;
    - edit with only `path` changed and an empty secret: 200, and the stored password is kept;
    - edit with `host` changed and an empty secret: 422 on `secret`;
    - edit to key auth: the stored secret is `""`.
  - `TestTypeCannotChange`: PUT `type:"s3"` on an SFTP destination gives 422 on `type`.
  - `TestTestRecordsHostKeyOnlyOnSuccess`:
    - a fake that fails, with `seen` set, leaves `HostKey` at `""`;
    - a fake that succeeds stores the key; the response `sftp.hostKey` starts with `ssh-ed25519 SHA256:`;
    - the audit event has the detail `hostKey`.
  - `TestResetHostKey`:
    - 200, `sftp.hostKey == ""`, `lastTestAt == null`;
    - the audit action is `backup_destination.reset_host_key` with the destination's name as target.
  - `TestChangingHostForgetsHostKey`: after the key is recorded, PUT another host with the password. The stored `HostKey` is `""` and `lastTestOk` is false.
  - Extend `TestResponsesNeverCarrySecret` with an SFTP password destination and a key destination. No body contains the password or `BEGIN OPENSSH PRIVATE KEY`.

- [ ] **Step 2: Run them and see them fail**

  Run: `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/backupdest/ ./internal/authz/`
  Expected: FAIL.

- [ ] **Step 3: Implement it and write ADR-0042**

  The ADR is "SFTP Backup destinations: a host key trusted on first Test". It records these decisions:
  - TOFU on Test only, with the key in its own column, forgotten on a host or port change, and Reset;
  - the algorithm pinning;
  - a lazy connection per operation, and why;
  - `.part` then rename;
  - the SSH-key delete refusal for destinations only, while Sources keep losing their key.

  Consequence: a host key is trusted without out-of-band verification, so the form shows the fingerprint to compare.

- [ ] **Step 4: Run the tests**

  Run: `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/backupdest/ ./internal/authz/ ./internal/httpapi/`
  Expected: PASS.

- [ ] **Step 5: Commit**: `feat: serve sftp destinations and reset host key`

---

### Task 4: Schedules, uploads and the SSH keys library

**Files:**
- Modify:
  - `internal/databases/uploads.go`: `dest.S3.Prefix` becomes `dest.KeyPrefix()`. This is the only Go reader of `.S3` outside `backupdest`, checked with `grep -rn '\.S3\b' internal/` on 2026-11-15;
  - `internal/databases/schedule_http.go`: refuse an untested SFTP destination;
  - `internal/sshkeys/store.go`, `internal/sshkeys/http.go`.
- Test:
  - `internal/databases/uploads_test.go`, `internal/databases/schedule_http_test.go`
  - `internal/sshkeys/store_test.go`, `internal/sshkeys/http_test.go`

**Interfaces:**
- Consumes: `Destination.KeyPrefix()`, `Destination.Type`, `Destination.HostKey` (Task 1).
- Produces:
  ```go
  // sshkeys.Key gains:
  UsedByDestinations []string // destination names, sorted
  // sshkeys:
  type ErrKeyInUse struct{ Destinations []string } // Error(): "used by Backup destinations: a, b"
  ```
  - JSON `usedByDestinations: string[]` in the Admin list. A Member's list stays names only.
  - `sshkeys` reads `backup_destinations` with SQL and does not import `backupdest`.

- [ ] **Step 1: Write the failing tests**
  - `TestUploadKeyUsesSFTPPath`: the fake opener returns `Destination{Type:"sftp", SFTP:{Path:"/srv/b"}}`, and the recorded `object_key` starts with `/srv/b/<slug>-<id>/`.
  - `TestScheduleRefusesUntestedSFTP`:
    - saving a schedule with an SFTP destination whose `HostKey == ""` is 422 with the exact text from Global Constraints;
    - with a host key set, it saves;
    - an S3 destination that was never tested still saves, as it does today.
  - `TestDeleteKeyUsedByDestination`:
    - a destination row with `ssh_key_id` set;
    - `DELETE /api/ssh-keys/{id}` gives 409 with `destinations: ["<name>"]`;
    - the key still exists.
  - `TestDeleteKeyUsedBySourceStillDetaches`: today's behaviour, unchanged.
  - `TestListShowsDestinationUse`: the Admin list has `usedByDestinations: ["<name>"]`, and the Member list has no such field.

- [ ] **Step 2: Run them and see them fail**

  Run: `env -u DOCKER_CONFIG -u DOCKER_HOST go test ./internal/databases/ ./internal/sshkeys/`
  Expected: FAIL.

- [ ] **Step 3: Implement it**

  `sshkeys.Store.Delete` checks destinations inside its transaction before anything else, and returns `ErrKeyInUse`. `writeStoreError` maps that to 409 with the list.

- [ ] **Step 4: Run all Go tests and vet**

  Run: `go vet ./... && env -u DOCKER_CONFIG -u DOCKER_HOST go test ./...`
  Expected: PASS.

- [ ] **Step 5: Commit**: `feat: schedule backups to tested sftp destinations`

---

### Task 5: Backup destinations and SSH keys pages

**Files:**
- Modify:
  - `web/src/data/types.ts`, `web/src/data/api.ts`: `resetBackupDestinationHostKey(id)`;
  - `web/src/pages/BackupDestinationsPage.tsx`, `web/src/pages/backup-destinations-view.ts`;
  - `web/src/pages/SshKeysPage.tsx`, `web/src/pages/ssh-keys-view.ts`;
  - `web/src/components/DatabaseScheduleCard.tsx`: the destination picker. An SFTP destination with `hostKey === ''` is disabled, with the label suffix "Test this destination first".
- Test: `web/src/pages/BackupDestinationsPage.test.tsx`, `web/src/pages/SshKeysPage.test.tsx`, `web/src/pages/ssh-keys-view.test.ts`, and the schedule card's test

**Interfaces:**
- Consumes: Task 3's JSON and Task 4's `usedByDestinations`.
- Produces:
  ```ts
  export interface BackupDestinationSftp { host: string; port: number; user: string; path: string;
    auth: 'key' | 'password'; sshKeyId: number | null; sshKeyName: string; publicKey: string; hostKey: string }
  export type BackupDestination = { id: number; name: string; lastTestAt: string | null; lastTestOk: boolean; lastTestError: string }
    & ({ type: 's3'; s3: BackupDestinationS3 } | { type: 'sftp'; sftp: BackupDestinationSftp });
  // BackupDestinationInput: same union; sftp input omits sshKeyName, publicKey, hostKey.
  // SshKey gains usedByDestinations: string[]
  ```

**UI:**
- **Add modal.** It opens with **Type**: S3-compatible or SFTP, as a `Segmented` or `Radio.Group`, and the type is fixed on edit.
- **SFTP fields** (the labels are the exact copy):
  - Host;
  - Port, which defaults to 22;
  - User;
  - Base path, with the extra text "Optional. Starts with / for an absolute path; otherwise relative to the login directory.";
  - Authentication, either "SSH key (recommended)" or "Password";
  - SSH key: a `Select` from `api.listSshKeys()`. Under it goes the chosen key's public key, `Text code copyable`, with the line "Add this line to ~/.ssh/authorized_keys on the server." If there are no keys, a link to the SSH keys page;
  - Password, with the same blank-keeps-it hint as S3's secret.
- **Edit modal.**
  - A **Host key** row shows `hostKey`, or "Not trusted yet. The next Test trusts the key the server presents.".
  - **Reset host key** is a `Popconfirm`: "Forget this host key? The next Test trusts whatever key the server presents."
- **Table location column.** `sftp://user@host:port/path`, with `:port` omitted when it is 22.
- **SSH keys page.**
  - The Used by column lists destinations as "Backup destination: <name>".
  - Delete is disabled for a key with `usedByDestinations.length > 0`. Its `Tooltip` reads "Used by Backup destinations: <names>. Change those destinations first."

- [ ] **Step 1: Write the failing tests**
  - Add SFTP with key auth:
    - choosing SFTP shows Host, Port (value 22), User, Base path and SSH key;
    - picking a key shows its public key;
    - submit calls `createBackupDestination` with `{type:'sftp', sftp:{host, port:22, user, path, auth:'key', sshKeyId}, secret:''}`.
  - Add SFTP with password auth: submitting with no password shows the field error and does not call the API.
  - Edit a destination with a host key: the fingerprint is shown, and Reset → OK calls `resetBackupDestinationHostKey(id)`.
  - Location column: `sftp://u@box:23/b` for port 23, and `sftp://u@box/b` for port 22.
  - SshKeysPage: a key with `usedByDestinations:['offsite']` shows "Backup destination: offsite", and its Delete button is disabled.

- [ ] **Step 2: Run them and see them fail**

  Run: `cd web && npx vitest run src/pages/BackupDestinationsPage.test.tsx src/pages/SshKeysPage.test.tsx src/pages/ssh-keys-view.test.ts`
  Expected: FAIL.

- [ ] **Step 3: Implement it**

- [ ] **Step 4: Run the web checks**

  Run: `cd web && npm run typecheck && npx vitest run && npm run build`
  Expected: all pass.

- [ ] **Step 5: Commit**: `feat: add sftp to the backup destinations page`

---

### Task 6: e2e against OpenSSH

**Files:**
- Create: `e2e/tests/sftp-destination.spec.ts`

Follow `e2e/tests/backup-destinations.spec.ts`: its `docker()` helper, `signIn`, `panelMinute` and the scheduled-backup wait. Use run-unique names for the server container (`e2e-sftp-<runId>`) and the destinations, so the spec ignores what another run left behind.

- [ ] **Step 1: Write the spec**

  **Setup:**
  - create an SSH key through the API;
  - run the pinned image on `relist-network` with `USER_NAME=e2e`, `PUBLIC_KEY=<its public key>`, `PASSWORD_ACCESS=true` and `USER_PASSWORD=<run-unique>`;
  - wait until `docker exec … pgrep sshd` succeeds.

  1. **Add and Test in the browser.** Add an SFTP destination with key auth: host = the container name, port 2222, user `e2e`, base path `backups`. Click Test. The row shows the test as passed (check it the way `backup-destinations.spec.ts` does), and the shown fingerprint equals one of these:

     ```
     docker exec <c> ssh-keygen -lf /config/ssh_host_keys/ssh_host_{ed25519,ecdsa,rsa}_key.pub
     ```

     Compare the `SHA256:` field.
  2. **A schedule copies.** Create Postgres 18, set its schedule one minute ahead with this destination, and wait for the Backup.
     - `docker exec <c> find /config/backups -type f` lists exactly `/config/backups/<slug>-<id>/<logical>/<file>`, with no `.part`;
     - its size equals the Backup's size.
  3. **The key cannot be deleted.** `DELETE /api/ssh-keys/{id}` gives 409.
  4. **Delete.** Deleting the Backup removes the file on the server: `find` lists nothing under `<slug>-<id>/`.
     - This step must come before step 5. Recreating the container wipes `/config`, and a delete of a file that is already missing passes without proving anything.
  5. **A changed host key.**
     - Recreate the container under the same name, which gives new host keys. Test gives `lastTestOk:false` with "host key changed".
     - Reset host key, then Test: it passes with a different fingerprint.
  6. **Password auth.** A second destination with password auth passes Test.

  Then clean up the Database, the destinations, the key and the container.

- [ ] **Step 2: Run it**

  Run: `task e2e:reload && task e2e -- tests/sftp-destination.spec.ts`
  Expected: PASS.

- [ ] **Step 3: Run the whole e2e suite**

  Run: `task e2e`
  Expected: all pass, 53 + the new spec.

- [ ] **Step 4: Commit**: `test: cover sftp destinations end to end`
