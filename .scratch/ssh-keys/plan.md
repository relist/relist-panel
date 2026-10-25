# SSH keys library Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the per-Source Deploy key with a host-wide SSH keys library that a Git Source picks one key from.

**Architecture:** A new `internal/sshkeys` package owns the `ssh_keys` table, key generation and `/api/ssh-keys`. `compose_sources` gains `ssh_key_id`. `sources.Store.Get` joins the key material into `Source.PrivateKey`/`PublicKey`, so the Git runner, Deployment snapshots and log redaction are unchanged. The web app gets an SSH keys page, and the Source modal gets a key `Select`.

**Tech Stack:** Go 1.x, `modernc.org/sqlite`, goose migrations, `golang.org/x/crypto/ssh`; React + antd 6.5.1, vitest + Testing Library.

**Spec:** `.scratch/ssh-keys/spec.md`

## Global Constraints

- Never edit committed migrations. The only schema change is the new `internal/migrations/00021_ssh_keys.sql`.
- No API response ever contains a private key.
- Keys are ed25519. The public key comment is `relist-<name>` at generation time.
- Names are trimmed and required, and unique ignoring case (`COLLATE NOCASE`, like `projects.name`).
- Copy, verbatim:
  - duplicate name: `An SSH key with this name already exists`
  - test without a key: `Choose an SSH key first`
  - build without a key: `Choose an SSH key for this Source`
  - card warning: `No SSH key — choose one before deploying.`
  - delete in use: `These Sources will have no SSH key and can't deploy until you choose another.`
  - generate hint: `Repository → Settings → Deploy keys → Add deploy key, leave write access off. A deploy key works for one repository; to reuse a key across repositories, add it to a machine user's account instead.`
- Commits: Conventional Commits with no scope, and no `Co-Authored-By` or `Claude-Session` trailers (`docs/agents/commit-conventions.md`).
- Frontend: before touching `web/`, read https://ant.design/llms.txt and the pages for Table, Select, Modal, Form, Typography (copyable) and Alert. Follow `docs/GUIDELINES.md`.
- Go tests: `go test ./...`. Web: `pnpm --dir web test`, `pnpm --dir web typecheck`.

## Decisions made while planning (differ from or sharpen the spec)

- A build queued without a key answers **400** (not 409). It sits next to the existing `reasonNoBranch` 400 guard in `internal/deployments/http.go`, and the 409 range there means "busy/conflict". `skip-build` and `deploy` don't fetch, so they are not blocked.
- Name uniqueness is case-insensitive (`COLLATE NOCASE`), matching `projects.name`.
- `PUT .../source` always takes `sshKeyId` as the full desired value (`null` or missing means no key). The modal therefore sends its current selection on every save, including the URL-blur save.
- Spec open question closed: server migration copies `panel.db` whole with `VACUUM INTO` (`internal/migration/items.go:128`), so `ssh_keys` travels with no change.
- The migration lock (`internal/migration/lock.go:434`) blocks every `/api/` path that isn't on its allowlist, so `/api/ssh-keys` is gated with no change.
- `spec.md` has been updated to match these decisions.

## Review Focus

1. **URL blur after picking a key**: the blur save must keep the chosen key, not clear it. The test is in Task 7.
2. **A key deleted while the Source modal is open**: saving with that stale id gets 400, and the modal reloads the key list and clears the selection. The test is in Task 7.
3. **Whitespace and case variants of names**: `"  "` is 400; `"ci"` after `"CI"` is 409, both on create and on rename. The tests are in Tasks 1 and 2.
4. **skip-build and zip without a key**: neither is blocked; only a Git `build` is. The test is in Task 4.
5. **A queued Deployment after its key is deleted**: it still fetches with the snapshot's key. A legacy (no-snapshot) resume whose Source now has no key fails at fetch with `Choose an SSH key for this Source` and does not panic. The tests are in Task 4.

---

### Task 1: `ssh_keys` table, migration and store

**Files:**
- Create: `internal/migrations/00021_ssh_keys.sql`
- Create: `internal/sshkeys/keys.go`: move `GenerateKey` from `internal/sources/keys.go` and leave the sources copy in place until Task 3.
- Create: `internal/sshkeys/keys_test.go`: copy `internal/sources/keys_test.go`, with the package renamed.
- Create: `internal/sshkeys/store.go`
- Create: `internal/sshkeys/store_test.go`
- Modify: `internal/database/database_test.go`: add a migration test.

**Interfaces:**
- Produces:
  ```go
  package sshkeys
  type Key struct { ID int64; Name, PrivateKey, PublicKey string; CreatedAt time.Time; UsedBy []Use }
  type Use struct { ProjectID int64; ProjectName string; EnvironmentID int64; ComposeID int64; ComposeName string }
  var ErrNameTaken = errors.New("An SSH key with this name already exists")
  var ErrNameRequired = errors.New("name is required")
  func GenerateKey(comment string) (privatePEM, authorizedKey string, err error)
  func NewStore(db *sql.DB) *Store
  func (s *Store) List(ctx) ([]Key, error)                    // ordered by name; UsedBy filled, ordered by project, compose name
  func (s *Store) Get(ctx, id int64) (Key, error)             // sql.ErrNoRows when missing; UsedBy filled
  func (s *Store) Create(ctx, name string) (Key, error)       // trims; generates with comment "relist-"+name
  func (s *Store) Rename(ctx, id int64, name string) (Key, error)
  func (s *Store) Delete(ctx, id int64) error                 // sql.ErrNoRows when missing
  ```

- [ ] **Step 1: Write the migration test** `TestSSHKeysMigrationDiscardsSourceKeys` in `database_test.go`, following `TestComposeOwnedSetsMigrationMovesEnvironmentSets`:
  1. `goose.UpTo(db, ".", 20)`.
  2. Insert a project, environment and compose, and a `compose_sources` git row with `ssh_private_key='PRIV'`, `ssh_public_key='PUB'`, `last_test_at=5`, `last_test_ok=1`.
  3. `goose.Up`.
  4. Assert that `ssh_private_key=''`, `ssh_public_key=''`, `last_test_at IS NULL`, `last_test_ok IS NULL` and `ssh_key_id IS NULL`, and that `ssh_keys` exists and is empty.
- [ ] **Step 2: Run it.** `go test ./internal/database -run TestSSHKeysMigration` should FAIL because `ssh_key_id` doesn't exist.
- [ ] **Step 3: Write the migration.** Use the SQL from the spec's Data model section, with `name TEXT NOT NULL COLLATE NOCASE UNIQUE`. Add a comment that `ssh_private_key`/`ssh_public_key` are retired and nothing reads them. The `-- +goose Down` section drops `ssh_key_id` and the table.
- [ ] **Step 4: Run it again.** The same command should PASS.
- [ ] **Step 5: Write the store tests** in `store_test.go`, with a fixture like `sources/store_test.go:newDB` that adds two composes `a` and `b` in project `p`, env `e`:
  - `TestCreateTrimsAndGenerates`: `Create(ctx, "  ci  ")` has Name `ci`, a PublicKey starting `ssh-ed25519 ` and ending ` relist-ci`, a PrivateKey containing `OPENSSH PRIVATE KEY`, and an empty UsedBy.
  - `TestCreateRejectsBlankAndDuplicate`: `"  "` gives `ErrNameRequired`. After `"CI"`, `"ci"` gives `ErrNameTaken`.
  - `TestRename`: a rename to a free name sticks and the PublicKey is unchanged. A rename onto another key's name (in any case) gives `ErrNameTaken`. A rename to `""` gives `ErrNameRequired`. An unknown id gives `sql.ErrNoRows`.
  - `TestListUsedBy`: set `compose_sources.ssh_key_id` for `a` and `b` directly with SQL. `List` returns the key with UsedBy `[{p, a}, {p, b}]` and the ids filled in.
  - `TestDeleteDetachesSources`: compose `a` uses the key and has `last_test_ok=1`. After `Delete`, `a` has `ssh_key_id` NULL and `last_test_at`/`last_test_ok` NULL. A second `Delete` gives `sql.ErrNoRows`.
- [ ] **Step 6: Run them.** `go test ./internal/sshkeys` should FAIL to compile.
- [ ] **Step 7: Implement `store.go`.** Map the unique-constraint error to `ErrNameTaken` with a local `isUniqueConstraint(err)`, a copy of `internal/projects/projects.go:417`. `Delete` runs one transaction that clears `last_test_*` on the Sources using the key, then deletes the key; `ON DELETE SET NULL` detaches them. The UsedBy join is `compose_sources s JOIN composes c ON c.id = s.compose_id JOIN environments e ON e.id = c.environment_id JOIN projects p ON p.id = e.project_id`.
- [ ] **Step 8: Run the tests again.** `go test ./internal/sshkeys ./internal/database` should PASS.
- [ ] **Step 9: Commit.** `feat: add the ssh_keys table and store`

### Task 2: `/api/ssh-keys` routes

**Files:**
- Create: `internal/sshkeys/http.go`, `internal/sshkeys/http_test.go`
- Modify: `internal/httpapi/httpapi.go:114`: register `sshkeys.New(db).RegisterRoutes(mux)` next to `sources.New(...)`.

**Interfaces:**
- Consumes: Task 1's `Store`, `ErrNameTaken`, `ErrNameRequired`.
- Produces: `func New(db *sql.DB) *Service` and `func (s *Service) RegisterRoutes(mux *http.ServeMux)`. The JSON is
  `{id, name, publicKey, createdAt, usedBy: [{projectId, projectName, environmentId, composeId, composeName}]}`, where `usedBy` is always an array and never null. Errors use `{"error": msg}` like the other packages: copy the `writeJSON`/`writeError` helpers from `internal/domains/store.go:387`.

- [ ] **Step 1: Write the failing tests** in `http_test.go`, using `httptest` with the mux:
  - `TestCreateListRenameDelete`: POST `{"name":"ci"}` gives 201 with `usedBy: []`. GET gives 200 with one item. PATCH `{"name":"deploy"}` gives 200 and the new name. DELETE gives 204. GET then returns `[]`.
  - `TestNoResponseContainsPrivateKey`: create a key, then run GET and PATCH. No response body contains `PRIVATE KEY` or `privateKey`.
  - `TestStatusCodes`: POST `{"name":" "}` → 400. A duplicate POST → 409 with body error `An SSH key with this name already exists`. PATCH on a name taken in another case → 409. PATCH/DELETE `/api/ssh-keys/999` → 404. A non-numeric id → 400. A body that isn't JSON → 400.
- [ ] **Step 2: Run them.** `go test ./internal/sshkeys -run 'TestCreateList|TestNoResponse|TestStatusCodes'` should FAIL.
- [ ] **Step 3: Implement `http.go`.** Routes: `GET /api/ssh-keys`, `POST /api/ssh-keys`, `PATCH /api/ssh-keys/{id}`, `DELETE /api/ssh-keys/{id}`. Cap bodies at `1 << 16`. Then wire the service in `httpapi.go`.
- [ ] **Step 4: Run the tests again.** `go test ./internal/sshkeys ./internal/httpapi` should PASS.
- [ ] **Step 5: Commit.** `feat: serve the SSH keys library at /api/ssh-keys`

### Task 3: Git Source chooses an SSH key

**Files:**
- Modify: `internal/sources/store.go`: add `SSHKeyID *int64` and `SSHKeyName string` to `Source`. `Get` reads `ssh_key_id` and LEFT JOINs `ssh_keys` into `PrivateKey`, `PublicKey` and `SSHKeyName`. `Save` writes `ssh_key_id` and stops writing `ssh_private_key`/`ssh_public_key`.
- Modify: `internal/sources/http.go`: `putInput.SSHKeyID *int64 \`json:"sshKeyId"\``. In `sourceJSON`, replace `PublicKey` with `SSHKey *sshKeyJSON \`json:"sshKey"\`` (`{id, name, publicKey}`). Remove `regenerateKey`, its route, `keyComment` and the generate-on-save block.
- Delete: `internal/sources/keys.go`, `internal/sources/keys_test.go`.
- Test: `internal/sources/store_test.go` and `internal/sources/http_test.go`. Two existing tests save a Source with `PrivateKey:` and expect it back. They now create a key with `sshkeys.NewStore(db).Create` and save `SSHKeyID` instead:
  - `internal/httpapi/sources_test.go:383-387`, which also calls `sources.GenerateKey`.
  - `internal/deployments/snapshot_test.go:121`.
  - `internal/sources/git_test.go:76` and `internal/deployments/redaction_test.go:85` build `Source` structs in memory and stay as they are.
- Callers of `Store.Save`: `put` (`http.go:250`) now sets `SSHKeyID` from the input. `regenerateKey` (`:328`) is removed. `upload` (`:594`) writes a zip Source, so losing the key there is correct.

**Interfaces:**
- Consumes: the `ssh_keys` table from Task 1. Tests may create keys with `sshkeys.NewStore(db).Create`.
- Produces: `sources.Source.SSHKeyID *int64` and `SSHKeyName string`. `PrivateKey`/`PublicKey` are now read-only, joined values (empty when there is no key). JSON: `sshKey: {id, name, publicKey} | null`.

- [ ] **Step 1: Write the failing tests:**
  - store: `TestGetJoinsTheChosenKey`: save with `SSHKeyID=&k.ID`, and `Get` returns the key's PrivateKey, PublicKey and Name. Without a key, `SSHKeyID` is nil and `PrivateKey` is `""`.
  - http: `TestPutWithKeyAndWithout`: a PUT with `"sshKeyId": k` returns `sshKey.id == k` and `sshKey.publicKey == k.PublicKey`. A PUT without the field returns `"sshKey": null`.
  - http: `TestPutUnknownKeyIs400`: `"sshKeyId": 999` gives 400.
  - http: `TestChangingKeyClearsTheTest`: record a test, then PUT the same URL with another key, and `lastTest` is null. A PUT with the same key and same URL keeps `lastTest`.
  - http: `TestTestWithoutKeyIs400`: POST `/test` on a git Source with no key gives 400 `Choose an SSH key first`, and `git.ListBranches` is not called.
  - http: `TestKeyRouteIsGone`: POST `/source/key` gives 404 or 405.
- [ ] **Step 2: Run them.** `go test ./internal/sources` should FAIL.
- [ ] **Step 3: Implement.** Before saving, validate `sshKeyId` with `SELECT 1 FROM ssh_keys WHERE id = ?` and answer 400 `unknown SSH key`. Keep `LastTest*`/`HeadSHA` only when the URL is unchanged **and** the key id is unchanged; the branch rule stays as it is. The `test` handler checks `src.PrivateKey == ""` before calling git.
- [ ] **Step 4: Run everything.** `go test ./...` should PASS. This includes `internal/deployments` and `internal/migration`, which build `sources.Source` values.
- [ ] **Step 5: Commit.** `feat: let a Git Source choose a key from the SSH keys library`

### Task 4: Deployments need a key to build from Git

**Files:**
- Modify: `internal/deployments/queue.go:107` and the fetch guard near `:790`
- Modify: `internal/deployments/http.go:157`
- Test: `internal/deployments/queue_test.go`, `internal/deployments/snapshot_integration_test.go`, plus the HTTP test file that covers `reasonNoBranch`. Find it with `grep -rn reasonNoBranch internal/deployments/*_test.go`, or use `queue_test.go`.

**Interfaces:**
- Consumes: Task 3's `Source.PrivateKey`, which is joined and empty when there is no key.
- Produces: `reasonNoKey = "Choose an SSH key for this Source"`.

- [ ] **Step 1: Write the failing tests:**
  - `TestBuildWithoutKeyIs400`: a Git Source with a branch and no key, `POST /deployments {"mode":"build"}` gives 400 with error `Choose an SSH key for this Source`. The same Compose with `{"mode":"skip-build"}` is not refused for the key. A zip Source with `build` is not refused for the key.
  - `TestQueuedBuildKeepsKeyAfterDelete`: queue a build with a key, delete the key with `sshkeys.NewStore(db).Delete`, then run the job. The fake Fetcher receives a `Source` whose `PrivateKey` is the original key.
  - `TestLegacyResumeWithoutKeyFailsAtFetch`: follow the existing legacy-snapshot test pattern (`legacySnapshotWarning`). With a Git Source that has a branch and no key, the run fails at step `fetch` with reason `Choose an SSH key for this Source`.
- [ ] **Step 2: Run them.** `go test ./internal/deployments -run 'WithoutKey|KeepsKey'` should FAIL.
- [ ] **Step 3: Implement.** Add `reasonNoKey`. In `http.go`, add the case `in.Mode == ModeBuild && src.Kind == sources.KindGit && src.PrivateKey == ""` right after the no-branch case. In `queue.go`, add a `src.PrivateKey == ""` check after the `src.Branch == ""` check that returns `failed(stepFetch, reasonNoKey)`.
- [ ] **Step 4: Run all tests.** `go test ./...` should PASS.
- [ ] **Step 5: Commit.** `feat: refuse a Git build until its Source has an SSH key`

### Task 5: Web data layer and `ssh-keys-view`

**Files:**
- Modify: `web/src/data/types.ts:633-649`
- Modify: `web/src/data/api.ts:328-360`
- Create: `web/src/pages/ssh-keys-view.ts`, `web/src/pages/ssh-keys-view.test.ts`
- Test: `web/src/data/api.test.ts`, and fix the fixtures that use `publicKey` in `source-view.test.ts`, `useComposeSource.test.tsx`, `ComposePage.test.tsx` and `SourceModal.test.tsx`. They become `sshKey: null` or `sshKey: {id: 1, name: 'ci', publicKey: 'ssh-ed25519 AAAA relist-ci'}`.

**Interfaces:**
- Produces:
  ```ts
  export interface SshKeyUse { projectId: number; projectName: string; environmentId: number; composeId: number; composeName: string }
  export interface SshKey { id: number; name: string; publicKey: string; createdAt: string; usedBy: SshKeyUse[] }
  // ComposeSource: replace publicKey with  sshKey: { id: number; name: string; publicKey: string } | null
  // SourceInput: add  sshKeyId: number | null
  api.listSshKeys(): Promise<SshKey[]>                 // GET /api/ssh-keys
  api.createSshKey(name: string): Promise<SshKey>      // POST
  api.renameSshKey(id: number, name: string): Promise<SshKey>   // PATCH
  api.deleteSshKey(id: number): Promise<void>          // DELETE
  // api.regenerateKey removed
  // ssh-keys-view.ts
  export function usedByLabel(key: SshKey): string     // 'Not used' | 'a' | 'a, b' | 'a, b +2 more' (first two compose names)
  export function shortPublicKey(publicKey: string): string  // 'ssh-ed25519 AAAA…WXYZ relist-ci': type, first 4 + '…' + last 4 of the base64, comment
  export function deleteWarning(key: SshKey): string | null  // the in-use copy from Global Constraints when usedBy is non-empty, else null
  ```

- [ ] **Step 1: Write the failing tests** in `ssh-keys-view.test.ts` for each function at 0, 1, 2 and 4 uses. Include a short-key edge case: base64 of 8 characters or fewer is returned whole. Add an `api.test.ts` case that `createSshKey('ci')` POSTs `{"name":"ci"}` to `/api/ssh-keys`.
- [ ] **Step 2: Run them.** `pnpm --dir web test ssh-keys-view api` should FAIL.
- [ ] **Step 3: Implement** the types, the api functions and the view helpers.
- [ ] **Step 4: Run tests and typecheck.** `pnpm --dir web test && pnpm --dir web typecheck` should PASS. The typecheck fails until every `publicKey` and `regenerateKey` use is gone. `SourceModal.tsx` may get a minimal compile fix here (read `source.sshKey?.publicKey`); Task 7 does the real UI.
- [ ] **Step 5: Commit.** `feat: add the SSH keys API client and view helpers`

### Task 6: SSH keys page and menu

**Files:**
- Create: `web/src/pages/SshKeysPage.tsx`, `web/src/pages/SshKeysPage.test.tsx`
- Modify: `web/src/app/paths.ts`: `sshKeys: '/ssh-keys'`, plus `{ path: 'ssh-keys' }` in `routePaths`.
- Modify: `web/src/app/routes.tsx:33`: `'ssh-keys': <SshKeysPage />`.
- Modify: `web/src/app/AppShell.tsx:113-170`: a menu item `{ key: 'ssh-keys', icon: <KeyOutlined />, label: 'SSH keys' }` after `images`, its navigate branch, and the breadcrumb `Overview › SSH keys`, following the `images` lines.

**Interfaces:**
- Consumes: Task 5's api and view helpers.

- [ ] **Step 1: Write the failing tests** in `SshKeysPage.test.tsx`. Mock `api` like `SourceModal.test.tsx` does.
  - The table renders the name, `shortPublicKey`, `usedByLabel` (as links to `paths.compose(...)`) and the creation date. With an empty list it shows an empty state that has a Generate key button.
  - Generate: fill in Name `ci` and submit. `createSshKey('ci')` is called, then the modal shows the full public key with a copy control and the generate hint. Closing the modal reloads the list.
  - Generate gets a 409: `ApiError` 409 with message `An SSH key with this name already exists`, and the message appears under the Name field.
  - Rename: the modal is prefilled, submit calls `renameSshKey(id, name)`, and a 409 shows under the field.
  - Delete an unused key: `DangerModal` confirms and `deleteSshKey(id)` is called. Delete an in-use key: the modal lists the compose names and the in-use warning copy.
- [ ] **Step 2: Run them.** `pnpm --dir web test SshKeysPage` should FAIL.
- [ ] **Step 3: Implement the page.** Use antd `Table`, `Modal` + `Form` (required `name`), `Typography.Paragraph copyable` and the existing `DangerModal`. Match the layout of `ImagesPage.tsx`. Then add the paths, route and AppShell entries.
- [ ] **Step 4: Run tests and typecheck.** `pnpm --dir web test && pnpm --dir web typecheck` should PASS.
- [ ] **Step 5: Commit.** `feat: add the SSH keys page`

### Task 7: Source modal key Select and card warning

**Files:**
- Modify: `web/src/components/SourceModal.tsx`: the Deploy key block (`:216-226`), the regenerate button (`:290-305`), `publicKey` state (`:53,77,100,118,162`), and `persistUrl`.
- Modify: `web/src/data/source-view.ts`: add `noKey: boolean` to `SourceCardState`; it is true for a git Source with `sshKey === null`.
- Modify: `web/src/components/SourceCard.tsx`: show `<Alert type="warning" showIcon title="No SSH key — choose one before deploying." />` when `state.noKey`. Check the `Alert` title/message prop name against the antd 6.5.1 docs.
- Test: `web/src/components/SourceModal.test.tsx`, `web/src/data/source-view.test.ts`, and a `SourceCard` warning case in whichever test renders the card (`ComposePage.test.tsx`).

**Interfaces:**
- Consumes: Task 5's `api.listSshKeys`, `SourceInput.sshKeyId` and `ComposeSource.sshKey`.

- [ ] **Step 1: Write the failing tests** in `SourceModal.test.tsx`. Add `listSshKeys` to the mocked api and remove `regenerateKey`.
  - When the modal opens, `listSshKeys` is called, and the `SSH key` Select lists the key names. Choosing `ci` calls `saveSource` with `sshKeyId: 1`, and the public key of `ci` then shows with a copy control.
  - **Review Focus 1:** with key `ci` chosen, editing the URL and blurring calls `saveSource` with `sshKeyId: 1`.
  - **Review Focus 2:** `saveSource` rejects with `ApiError` 400 `unknown SSH key`. `listSshKeys` is called again and the Select has no value.
  - With an empty key list, the modal shows `No SSH keys yet` and a `Create one in SSH keys` link with `href="/ssh-keys"` and `target="_blank"`. A reload button calls `listSshKeys` again.
  - Test connection is disabled when the URL is saved but no key is chosen, and enabled once both are set.
  - There is no `Regenerate key` button.
  - `source-view.test.ts`: `noKey` is true for git with `sshKey: null`, false for git with a key, and false for zip.
  - Card: a git Source without a key renders the warning copy.
- [ ] **Step 2: Run them.** `pnpm --dir web test SourceModal source-view ComposePage` should FAIL.
- [ ] **Step 3: Implement.** The modal keeps `sshKeyId: number | null` state, seeded from `current?.sshKey?.id ?? null`. Every `saveSource` call includes it. The public key shown comes from the chosen item in the loaded list. Remove `regenerate`. Keep the `Reset host key` action.
- [ ] **Step 4: Run tests and typecheck.** `pnpm --dir web test && pnpm --dir web typecheck` should PASS.
- [ ] **Step 5: Commit.** `feat: choose the Source's SSH key from the library`

### Task 8: Docs and embedded frontend

**Files:**
- Modify: `CONTEXT.md:43-45`: rename the term to **SSH key** with the spec's definition and `_Avoid_: Credential`. Update the "Deploy key" mentions in the Source and Deployment entries.
- Create: `docs/adr/0020-ssh-key-library.md`, in the format of `docs/adr/0016-sources-and-deployment-queue.md`. It says it supersedes ADR-0016's per-Compose Deploy key and links the spec.
- Modify: `docs/adr/0016-sources-and-deployment-queue.md`: add one line under Consequences: "Superseded in part by ADR-0020 (SSH key library)."
- Modify: `README.md`: rewrite the paragraph that mentions the deploy key (`grep -n -i "deploy key" README.md`).
- Modify: `.scratch/ssh-keys/spec.md`: set the `Status:` line to `closed — merged on branch feat/ssh-keys`, in the style of `.scratch/stability-review/spec.md`.
- Regenerate: the embedded frontend, with `go generate ./internal/webassets/...`.

- [ ] **Step 1: Write the docs changes above.**
- [ ] **Step 2: Rebuild the frontend.** Run `go generate ./internal/webassets/... && go build ./... && go test ./...`. Expected: PASS.
- [ ] **Step 3: Commit in two parts.** First the docs, `chore: document the SSH key library`. Then the rebuilt assets, `chore: rebuild the embedded frontend with the SSH keys page`.
