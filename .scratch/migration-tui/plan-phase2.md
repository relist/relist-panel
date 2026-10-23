# Migration TUI, phase 2: remove the web side — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Migration can only be driven from the terminal: the web pages and the TCP routes that create tokens, resume the old host and drive a pull are gone, which closes the stolen-session way out (spec, Problem, second paragraph).

**Architecture:** The handlers stay; only where they are mounted changes. `Source.RegisterRoutes` (TCP) keeps the host-to-host routes and `GET /api/migration/status`; tokens and resume move to the control socket only, where they already are. `Target.RegisterRoutes` is deleted, so the pull routes exist only on the control socket. The web keeps the read-only lock page and a Server migration card that names the commands.

**Tech Stack:** Go 1.26 (`net/http` ServeMux), React 19 + Ant Design 6 (`web/`), Vitest, bash probe on `docker:dind`.

**Spec:** `.scratch/migration-tui/spec.md`, section "Phase 2: remove the web side", with Open questions 3 (Resume is terminal-only).

**Branch:** cut `feat/migration-tui-phase2` from `feat/migration-tui` (PR #11). The PR stacks on #11.

## Global Constraints

- Conventional Commits, no scope (`feat: ...`, `fix: ...`, `chore: ...`); no `Co-Authored-By`/`Claude-Session` trailers.
- Never edit a committed SQL migration under `internal/migrations/`. This plan needs none.
- Before any `web/` edit: read Ant Design's `llms.txt` (https://ant.design/llms.txt), the component pages it links for the components touched (Card, Result, Typography, Steps, Alert), and `docs/GUIDELINES.md`. Check props against the `antd` version in `web/package.json`.
- Routes removed from the TCP listener, exactly: all of `/api/migration/pull/*`; `POST /api/migration/tokens`; `DELETE /api/migration/tokens/current`; `POST /api/migration/resume`.
- Routes that stay on TCP, exactly: `GET /api/migration/status`, and the host-to-host routes `claim`, `preflight`, `start`, `items/{id}`, `items/{id}/digest`, `abort`, `finish`.
- The control socket's routes do not change (`RegisterControlRoutes` on both sides).
- `Target.InProgress` keeps refusing first-admin setup while connected (commit 6b5bbd4). Do not remove it with the web routes.
- Commands named in UI copy, verbatim: `docker compose exec panel relist-panel migration serve`, `docker compose exec panel relist-panel migration pull`, `docker compose exec panel relist-panel migration resume`.
- Leave the untracked `web/tsconfig.tsbuildinfo` alone.
- Probe runs never contact Let's Encrypt production (the probe's ACME CA already points at an unreachable address; keep it so).

## Review Focus

1. **A removed route falls through to the SPA.** `/api/migration/pull/progress` on TCP, signed in, must answer 404 JSON-less from the file server, never `index.html` with 200. Pinned in Task 2.
2. **The cookie exemption outlives the routes.** If `SessionPath` still lets `/api/migration/pull/` past the cookie check, an anonymous request reaches whatever the mux has there. Signed out, it must answer 401. Pinned in Task 2.
3. **A panel upgraded mid-Migration.** An old host locked under phase 1 boots phase 2: its lock page must still show the step and name `migration resume`, and the socket's resume must unlock it. Pinned in Task 4 (lock page) and existing `TestControl*` tests (socket).
4. **The lock page after a terminal resume.** The page must leave by itself once `status.step` is `''`, without a reload. `useMigrationLock` already stops polling and re-renders on `''`; Task 4 pins it with a test.
5. **An empty panel with no web way in.** The setup screen loses its "Migrate from another panel" link; it must say which command to run instead, or the feature is invisible on a fresh install. Pinned in Task 3.

---

### Task 1: The old host's token and resume routes leave TCP

**Files:**
- Modify: `internal/migration/source_http.go:19-31` (`RegisterRoutes`)
- Modify: `internal/migration/source_http_test.go` (fixture at 28-55, tests at 59, 94, 118, 274-288)
- Modify: `internal/migration/integration_test.go` (fixture 67-135, tests at 234, 380-395)
- Modify: `internal/httpapi/migration_test.go:131-190` (`TestMigrationRoutesAuthWiring`), `internal/httpapi/migration_control_test.go:89-101` (`TestWebTokensStillNeedTheCookie`)

**Interfaces:**
- Produces: `(*Source).RegisterRoutes(mux)` registers only `claim`, `preflight`, `start`, `items/{id}`, `items/{id}/digest`, `abort`, `finish`, `status`. `RegisterControlRoutes` unchanged.
- Produces (tests): `sourceServer.ctl(t, method, path string, body any) *httptest.ResponseRecorder` serving a second mux built with `RegisterControlRoutes`; `panel.ctl(method, path string, body any) (int, []byte)` in the integration fixture, serving `opts.Control`, a `http.NewServeMux()` that `boot` creates and passes to `httpapi.NewHandler`.

- [ ] **Step 1: Write the failing test** in `internal/migration/source_http_test.go`:

```go
func TestTokenAndResumeRoutesOnlyOnTheControlMux(t *testing.T) {
	s := newSourceServer(t)
	for _, r := range []struct{ method, path string }{
		{"POST", "/api/migration/tokens"},
		{"DELETE", "/api/migration/tokens/current"},
		{"POST", "/api/migration/resume"},
	} {
		if rec := s.do(t, r.method, r.path, "", map[string]string{"lifetime": "24h", "confirm": "RESUME"}); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s on the web mux = %d, want 404", r.method, r.path, rec.Code)
		}
	}
	if rec := s.ctl(t, "POST", "/api/migration/tokens", map[string]string{"lifetime": "24h"}); rec.Code != http.StatusCreated {
		t.Fatalf("token over the control mux = %d %s", rec.Code, rec.Body)
	}
	if rec := s.do(t, "GET", "/api/migration/status", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("status on the web mux = %d, want 200", rec.Code)
	}
}
```

Add `ctl` and the control mux to `newSourceServer` in this step, so the test fails on the 404 assertions, not on compilation.

- [ ] **Step 2: Run it**

Run: `go test ./internal/migration/ -run TestTokenAndResumeRoutesOnlyOnTheControlMux`
Expected: FAIL, three lines `... on the web mux = 201/400/204, want 404` (codes as the handlers answer).

- [ ] **Step 3: Drop the three registrations from `RegisterRoutes`**, and update its doc comment: the web mux carries what moves data between hosts and the lock page's status; tokens and resume are the control socket's (ADR-0019).

- [ ] **Step 4: Move the callers to the control mux.** In `source_http_test.go`, every `s.do(... "/api/migration/tokens" ...)` and `.../resume` becomes `s.ctl(...)`; `TestItemsWaitForTheLockToSettleAfterARestart` (line 336) only needs `RegisterRoutes`, leave it. In `integration_test.go`, `boot` builds `p.control = http.NewServeMux()`, sets `opts.Control = p.control`, and the token (line 234) and resume (line 387) calls use `p.ctl`. In `httpapi/migration_test.go`, `TestMigrationRoutesAuthWiring` creates its token through `newControlServer`'s socket client (`controlDo`) instead of the cookie, and its first assertion becomes: signed out, `POST /api/migration/tokens` = 401; signed in = 404. Rename `TestWebTokensStillNeedTheCookie` to `TestWebHasNoTokenRoute` and assert 404 signed in.

- [ ] **Step 5: Run the packages**

Run: `go test ./internal/migration/ ./internal/httpapi/`
Expected: `ok` for both.

- [ ] **Step 6: Commit**

```bash
git add internal/migration/source_http.go internal/migration/source_http_test.go internal/migration/integration_test.go internal/httpapi/migration_test.go internal/httpapi/migration_control_test.go
git commit -m "feat: create and revoke migration tokens and resume the old host only from the terminal"
```

### Task 2: The pull routes leave TCP

**Files:**
- Modify: `internal/migration/target_http.go:10-68` (delete `RegisterRoutes` and `selfURL`; `connectRoute` keeps only the control socket's `selfUrl` validation)
- Modify: `internal/migration/source_http.go:58-67` (`SessionPath`)
- Modify: `internal/httpapi/httpapi.go:141-147` (drop `target.RegisterRoutes`), `internal/auth/auth.go:53-56` (comment)
- Modify: `internal/migration/integration_test.go` (lines 160, 247-250, 360), `internal/httpapi/migration_test.go:51-70, 94-130`
- Test: `internal/httpapi/migration_test.go`

**Interfaces:**
- Consumes: `panel.ctl` from Task 1.
- Produces: `(*Target).RegisterControlRoutes(mux)` is the only way the pull routes are mounted. `connectRoute()` takes no argument. `SessionPath` no longer matches `/api/migration/pull/`.

- [ ] **Step 1: Write the failing tests** in `internal/httpapi/migration_test.go`, replacing `TestPullRoutesOpenOnlyBeforeAdminExists`:

```go
func TestPullRoutesAreNotOnTheWeb(t *testing.T) {
	s := newMigrationServer(t)
	response := s.request(t, http.MethodGet, "/api/migration/pull/progress", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("progress before setup = %d, want 401: no route lets itself past the cookie", response.StatusCode)
	}
	setupAndLogin(t, s)
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/api/migration/pull/progress"},
		{http.MethodPost, "/api/migration/pull/connect"},
		{http.MethodPost, "/api/migration/pull/start"},
		{http.MethodPost, "/api/migration/pull/abort"},
	} {
		response := s.request(t, r.method, r.path, nil)
		body := readBody(t, response)
		if response.StatusCode != http.StatusNotFound || strings.Contains(body, "<html") {
			t.Errorf("%s %s signed in = %d %.40q, want a 404 that is not the app", r.method, r.path, response.StatusCode, body)
		}
	}
}
```

`TestStartConflictCarriesBlockingMessage` and `TestProgressAfterRestartReadsSummary` move to the control socket (`newControlServer` + `controlDo`); connect's body gains `"selfUrl":"https://new.example.com"`, and the https assertion stays.

- [ ] **Step 2: Run them**

Run: `go test ./internal/httpapi/ -run 'TestPullRoutesAreNotOnTheWeb|TestStartConflict|TestProgressAfterRestart'`
Expected: `TestPullRoutesAreNotOnTheWeb` FAILS (`progress before setup = 200, want 401`); the moved tests pass already, since the socket has the routes.

- [ ] **Step 3: Remove the TCP mounting.** Delete `Target.RegisterRoutes` and `selfURL`; fold the selfUrl check into `connectRoute()`; remove the `target.RegisterRoutes(...)` call in `httpapi.go`; drop the `/api/migration/pull/` prefix from `SessionPath` and its doc comment's "and the new host's pull routes" clause; reword `AllowSelfAuthenticated`'s comment to "a server migration's session-key routes".

- [ ] **Step 4: Move the integration test's pull calls** (lines 160, 247, 250, 360) to `p.new.ctl`, with `selfUrl` in connect's body.

- [ ] **Step 5: Run everything Go**

Run: `go vet ./... && go test ./...`
Expected: `ok` for every package.

- [ ] **Step 6: Commit**

```bash
git add internal/migration/target_http.go internal/migration/source_http.go internal/httpapi/httpapi.go internal/auth/auth.go internal/migration/integration_test.go internal/httpapi/migration_test.go
git commit -m "feat: drive the pull only over the control socket, and let no pull route past the web's cookie check"
```

### Task 3: The web loses the pull page

**Files:**
- Delete: `web/src/pages/MigrationPage.tsx`, `web/src/pages/MigrationPage.test.tsx`
- Modify: `web/src/app/routes.tsx:70-71`, `web/src/app/paths.ts:18` (+ `paths.test.ts` if it lists `migration`)
- Modify: `web/src/pages/AuthPages.tsx:366-370` (setup screen)
- Modify: `web/src/data/api.ts:218-228`, `web/src/data/migration.ts`, `web/src/data/migration.test.ts`
- Test: the setup screen's test file (find it with `grep -ln "Create admin account" web/src`)

**Interfaces:**
- Produces: `api` keeps `migrationStatus` only. `data/migration.ts` keeps `MigrationStep` and `MigrationStatus`; every pull type (`MigrationPhase` … `MigrationProgress`), `TokenLifetime` and `MigrationToken` go once nothing imports them (`ServerMigrationCard` stops in Task 4; delete those two types there if this task cannot yet).

- [ ] **Step 1: Write the failing test** on the setup screen:

```tsx
it('names the pull command instead of linking to a migration page', async () => {
  // render the setup screen as its existing tests do (setupRequired: true)
  expect(await screen.findByText('docker compose exec panel relist-panel migration pull')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Migrate from another panel' })).not.toBeInTheDocument();
});
```

- [ ] **Step 2: Run it**

Run: `pnpm --dir web exec vitest run <that test file>`
Expected: FAIL, the command text is not found.

- [ ] **Step 3: Replace the link** with a secondary paragraph under the form: "Moving a panel here? Don't create an admin. Run this on this server instead:" followed by `<Text code copyable>docker compose exec panel relist-panel migration pull</Text>`.

- [ ] **Step 4: Delete the page, its route and path, the pull and token/resume calls in `api.ts`, and their cases in `migration.test.ts`** (keep the status case). `pnpm --dir web typecheck` lists anything still importing them.

- [ ] **Step 5: Run the web suite**

Run: `pnpm --dir web typecheck && pnpm --dir web exec vitest run`
Expected: typecheck clean; every test passes.

- [ ] **Step 6: Commit**

```bash
git add -A web/src
git commit -m "feat: drop the web pull page and say on the setup screen which command migrates into this panel"
```

### Task 4: Read-only lock page and a command-only migration card

**Files:**
- Modify: `web/src/pages/MigrationLockPage.tsx` (+ `.test.tsx`), `web/src/app/RequireAuth.tsx` (the `onResumed` prop it passes)
- Modify: `web/src/components/ServerMigrationCard.tsx` (+ `.test.tsx`), `web/src/pages/DockerPage.test.tsx` if it asserts card copy
- Modify: `internal/webassets/dist/**` (rebuilt, separate commit)

**Interfaces:**
- Consumes: `MigrationStatus`, `api.migrationStatus` (Task 3 kept them).
- Produces: `MigrationLockPage({ status }: { status: MigrationStatus })`, no `onResumed`. `ServerMigrationCard()` takes no props and calls no API.

- [ ] **Step 1: Write the failing tests.**

`MigrationLockPage.test.tsx`:

```tsx
it('names the resume command and offers no button while locked', () => {
  render(<MigrationLockPage status={{ ...locked, step: 'stopping' }} />);
  expect(screen.getByText('docker compose exec panel relist-panel migration resume')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /Cancel migration|Resume this server/ })).not.toBeInTheDocument();
});
it('names the resume command once migrated', () => {
  render(<MigrationLockPage status={{ ...locked, step: 'migrated', targetUrl: 'https://new.example.com' }} />);
  expect(screen.getByText('docker compose exec panel relist-panel migration resume')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Resume this server' })).not.toBeInTheDocument();
});
```

A `RequireAuth` test (in its existing test file, or `AppShell.test.tsx` if that is where the lock is exercised): `migrationStatus` resolves `{ step: 'locked', … }` then `{ step: '', … }`; with fake timers advanced 5 s, the lock page's title "This server is being migrated" disappears and the app renders.

`ServerMigrationCard.test.tsx`: the card shows both `…migration serve` and `…migration pull`, has no "Create migration token" button, and `api.createMigrationToken` is not referenced (the mock no longer defines it).

- [ ] **Step 2: Run them**

Run: `pnpm --dir web exec vitest run src/pages/MigrationLockPage.test.tsx src/components/ServerMigrationCard.test.tsx src/app`
Expected: the new lock-page and card tests FAIL (buttons and token form still there). The RequireAuth test may pass already; it pins Review Focus 4.

- [ ] **Step 3: Implement.** Lock page: drop `DangerModal`, the buttons, `resume` and the error Alert; under the steps (locked) or the subtitle (migrated) add "To cancel the migration / To run this server again, run on this server:" with `<Text code copyable>docker compose exec panel relist-panel migration resume</Text>`. Keep the http(s)-only link to `targetUrl`. Card: the paragraph says to move this panel elsewhere run `…migration serve` here and `…migration pull` on the new server, both as copyable code; it keeps no state and no API call. Delete `TokenLifetime` and `MigrationToken` if Task 3 left them.

- [ ] **Step 4: Run the web suite**

Run: `pnpm --dir web typecheck && pnpm --dir web exec vitest run`
Expected: clean; all pass.

- [ ] **Step 5: Commit, then rebuild the embedded frontend**

```bash
git add -A web/src
git commit -m "feat: make the lock page and the migration card name the terminal commands instead of acting"
task build-web
git add -A internal/webassets/dist
git commit -m "chore: rebuild the embedded frontend without the web migration flow"
```

### Task 5: Probe, tooling and docs

**Files:**
- Modify: `scripts/probe-migration.sh:135-139`
- Delete: `scripts/dev-migration.sh`; Modify: `Taskfile.yml` (remove `dev:migration`), `web/vite.config.ts` (remove the `RELIST_PANEL_URL` proxy and its comment)
- Modify: `scripts/sandbox-migration.sh` (header comment and printout: no "Source URL to connect to" web hint; the TUI commands stay)
- Modify: `README.md:159-211`, `docs/adr/0018-server-migration.md:50-51`, `docs/adr/0019-migration-driven-from-the-terminal.md:26,46`, `.scratch/migration-tui/spec.md` (Status and Probe record)

- [ ] **Step 1: Change the probe first.** Token: `TOKEN=$(docker exec "$OLD" docker exec install-panel-1 relist-panel migration api POST /api/migration/tokens '{"lifetime":"1h"}' | json 'd["token"]')` (define an `oldctl()` beside `ctl()`). Replace line 138's 401 check, after the env admin signs in at 139, with: `GET /api/migration/pull/progress` on the new host = 404, and `POST /api/migration/tokens` on the old host with its cookie = 404.

- [ ] **Step 2: Run the probe**

Run: `task probe:migration 2>&1 | tail -5`
Expected: last line `probe: migration ok`.

- [ ] **Step 3: Tooling and docs.** Remove the dev script, task and proxy. README: steps 2–3 name only the commands; drop "or open Docker → Server migration", "Migrate from another panel", and "which signs the web page out"; the lock section says resume is `migration resume`. ADR-0018: replace the "Who can reach an empty panel can start a Migration into it" consequence with "Superseded by ADR-0019: a Migration is started from a shell in the container." ADR-0019: the phase lines say phase 2 is done (date). Spec: `Status: done`, and a Probe record line for this run.

- [ ] **Step 4: Verify**

Run: `go test ./... && pnpm --dir web typecheck && pnpm --dir web exec vitest run && bash -n scripts/sandbox-migration.sh scripts/probe-migration.sh`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add -A scripts Taskfile.yml web/vite.config.ts README.md docs .scratch/migration-tui/spec.md
git commit -m "docs: document migration as terminal-only, probe that the web routes are gone, and drop the dev servers"
```
