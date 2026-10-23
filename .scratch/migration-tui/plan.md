# Migration TUI (phase 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Drive a server Migration from a Bubble Tea TUI on both hosts (`relist-panel migration serve|pull|status|resume`). The TUI talks to the running panel over a Unix control socket, and finalize re-executes the panel instead of exiting, while the web pages keep working.

**Architecture:**
- The panel keeps doing the work (`Source`, `Target`, Finalizer, unchanged in behaviour).
- A second `http.ServeMux` holds the same handlers without cookie guards. It is served on `/run/relist-panel/control.sock`.
- A small typed client (`internal/migrationctl`) wraps that socket. Bubble Tea models (`internal/migrationtui`) and the commands (`internal/migrationcli`) sit on the client.
- Finalize's `os.Exit(0)` becomes a graceful shutdown followed by `syscall.Exec`.

**Tech Stack:** Go 1.26+, `charm.land/bubbletea/v2` v2.1.0, `charm.land/bubbles/v2` v2.2.1, `charm.land/lipgloss/v2` v2.0.6, `github.com/charmbracelet/x/term` (already pulled in by Bubble Tea).

**Spec:** `.scratch/migration-tui/spec.md`, phase 1 only. Phase 2 (removing the web side) is not in this plan.

## Global Constraints

- Commits: Conventional Commits, no scope (`feat: ...`, `fix: ...`, `chore: ...`, `docs: ...`, `test: ...`), and no `Co-Authored-By` or `Claude-Session` trailers.
- Never edit a committed goose migration. This plan needs no migration.
- Before any edit under `web/`, read Ant Design's `llms.txt` (https://ant.design/llms.txt) and `docs/GUIDELINES.md`. Check props against the installed `antd` 6.5.1.
- `go.mod`: `go 1.26.0`, which Bubble Tea v2 needs. Pin `charm.land/bubbletea/v2 v2.1.0`, `charm.land/bubbles/v2 v2.2.1`, `charm.land/lipgloss/v2 v2.0.6`.
- Control socket: default `/run/relist-panel/control.sock`, file mode `0600`, directory `0700`, overridable with `RELIST_CONTROL_SOCKET`.
- The TUI polls every 500 ms. `status --watch` reprints every 2 s.
- Without a terminal, `serve`, `pull` and `resume` exit with status 2 and print exactly: `needs a terminal: run it with docker compose exec, or docker exec -it`.
- Commands are documented as `docker compose exec panel relist-panel migration <cmd>`, with no container rename (spec open question 1, recommendation taken).
- Probe runs must never contact Let's Encrypt production. Keep the probe's `acme.caserver` override.
- Leave the untracked `web/tsconfig.tsbuildinfo` alone.

## Rulings made while planning

- **The probe cannot use `curl --unix-socket`.** The panel image (`docker:27-cli`) has only busybox `wget`, which has no Unix-socket support. The plan adds a hidden `relist-panel migration api METHOD PATH [JSON]` command. It does one raw request over the socket and prints the body. The probe and debugging use it. Cost if wrong: one small extra command to remove later.
- **`connect` over the socket takes `selfUrl` in the body.** The web derives it from the request's Host, but a socket request has no meaningful Host. The TUI asks for it, defaulting to `https://` + `RELIST_DOMAIN`.
- **A control socket that cannot be created only logs a warning.** For example, a bare binary run as a non-root user cannot write `/run`. The web panel must still come up.
- **`status` decides the side by asking both.** If the `Target`'s phase is not idle it is the new host. Otherwise, if the `Source`'s lock step is set or a token is live, it is the old host. Otherwise it prints "No migration on this host."

## Review Focus

1. **The control socket cannot be created** (bare binary as a non-root user, or `/run` read-only): the panel still serves HTTP and logs one warning. Covered by a test in Task 2.
2. **Two drivers at once** (the web page and a TUI, or two TUIs, say Abort during Retry): one gets the existing busy or conflict error, which the TUI shows as a line, and `state.json` stays consistent. Covered by tests in Tasks 2 and 5.
3. **A small terminal** (80×24) with 40 items: the pull view fits the window. The item list scrolls rather than pushing the journey line and the keys off the screen. Covered by a test in Task 5.
4. **A request in flight when the panel re-executes:** the client gets EOF or connection-refused. The TUI shows "Restarting the panel…" and keeps polling, and does not exit with an error. Covered by a test in Task 5.
5. **`status --watch` with no Migration on the host:** it prints "No migration on this host." once and exits 0, instead of looping. Covered by a test in Task 7.

---

### Task 1: Re-execute the panel at finalize; drop the restart-policy warning

**Files:**
- Modify: `internal/migration/finalize.go` (`FinalizeDeps.Exit` → `Restart`, default unchanged: `os.Exit(0)`)
- Modify: `internal/migration/options.go` (`Options.Exit` → `Restart func()`)
- Modify: `internal/httpapi/httpapi.go:136` (pass `Restart`)
- Modify: `internal/migration/preflight.go:228-231`, `internal/migration/docker.go:72-74`, `internal/dockerexec/migration.go:346`, `internal/migration/fakedocker_test.go:293`, `internal/dockerexec/migration_test.go:143`: remove `PanelRestartPolicy` and its warning
- Create: `restart.go` and `restart_test.go` (package `main`)
- Modify: `main.go` (restart channel, graceful shutdown, then re-execute)
- Modify tests: `internal/migration/{finalize,activity}_test.go` and `internal/httpapi/migration_test.go` (`Exit:` → `Restart:`)

**Interfaces:**
- Produces: `migration.FinalizeDeps.Restart func()` and `migration.Options.Restart func()`. Also `shutdown(ctx context.Context)` in `main.go`, used by both the signal path and the restart path. `main.reexec(exe string, args, env []string, exec func(string, []string, []string) error, exit func(int))`.

- [ ] **Step 1: Write the failing tests**
  - In `finalize_test.go`, `TestBeginRestartsAfterTheMarker`: with `Restart` recording a call that also checks `os.Stat(<state>/finalize)` succeeds, `Begin` calls `Restart` exactly once, after the marker exists.
  - In `restart_test.go`:
    - `TestReexecRunsTheSameBinaryWithTheSameArgs`: a fake exec records `(exe, args, env)` and returns nil. Assert they equal the inputs and that `exit` was not called.
    - `TestReexecFallsBackToExitOne`: the fake exec returns an error. Assert `exit(1)` is called.
  - In `preflight_test.go`, `TestPreflightNoLongerWarnsAboutTheRestartPolicy`: no warning contains `restart policy`.
- [ ] **Step 2: Run them.** Command: `go test ./internal/migration/ . -run 'Restart|Reexec|RestartPolicy'`. Expected: they fail to compile (`Restart` and `reexec` undefined) and the preflight assertion fails.
- [ ] **Step 3: Implement.**
  - Rename `Exit` to `Restart` everywhere listed above, and delete `PanelRestartPolicy` from the port, `dockerexec`, the fake, the preflight warning and its tests.
  - In `main.go`, pass `Restart: func() { restartOnce.Do(func() { close(restartCh) }); select {} }`. The finalize goroutine parks, and `main` does the shutdown.
  - Add a `case <-restartCh:` to the final `select`. It runs the same shutdown as the signal case. Extract that into one `shutdown(ctx)` func both cases call, so Task 2 can add the control server to it in one place. That shutdown is `server.Shutdown`, `stop()`, `sampler.Wait()`, `waitDeployments()` bounded by the 5 s deadline, then `db.Close()`. Then call `reexec(exe, os.Args, os.Environ(), syscall.Exec, os.Exit)`, where `exe` comes from `os.Executable()`.
  - In `reexec`, log the error and call `exit(1)` if exec fails.
  - Update the comment above `PrepareFinalize` in `main.go`: "re-executed itself" instead of "restarted".
- [ ] **Step 4: Run.** Command: `go test ./...`. Expected: all packages ok.
- [ ] **Step 5: Commit.** `feat: re-execute the panel at finalize instead of exiting, so the container and anything exec'd into it survive`

### Task 2: The control socket and the routes it serves

**Files:**
- Create: `internal/migration/control.go`, `internal/migration/control_test.go`
- Modify: `internal/migration/target_http.go` (add `RegisterControlRoutes`, and make `connectRoute` take the self URL from a function)
- Modify: `internal/migration/source_http.go` (add `RegisterControlRoutes`, and a `SourceStatus` struct that `status` encodes instead of the map)
- Modify: `internal/migration/options.go` (`Control *http.ServeMux`)
- Modify: `internal/httpapi/httpapi.go` (register the control routes when `migrationOptions.Control != nil`)
- Modify: `main.go` (listen, serve, shut down)
- Test: `internal/httpapi/migration_control_test.go`

**Interfaces:**
- Produces:
  - `migration.ControlSocketPath() string`: `RELIST_CONTROL_SOCKET`, or else `/run/relist-panel/control.sock`.
  - `migration.ListenControl(path string) (net.Listener, error)`: mkdir the parent `0700`, remove a stale file, listen on `unix`, chmod `0600`.
  - `(*Target).RegisterControlRoutes(mux *http.ServeMux)`: the six `/api/migration/pull/*` routes, unguarded. `connect` reads `{"sourceUrl","token","selfUrl"}` and requires `selfUrl` to be an http(s) URL.
  - `(*Source).RegisterControlRoutes(mux *http.ServeMux)`: `POST /api/migration/tokens`, `DELETE /api/migration/tokens/current`, `GET /api/migration/status`, `POST /api/migration/resume`, unguarded, with the same handlers as the web.
  - `migration.SourceStatus{Step Step; ClaimedBy, TargetURL string; TokenExpiresAt, LockedAt, MigratedAt *time.Time}`, with JSON names unchanged (`step`, `claimedBy`, `targetUrl`, `tokenExpiresAt`, `lockedAt`, `migratedAt`).

- [ ] **Step 1: Write the failing tests**
  - In `control_test.go`:
    - `TestListenControlReplacesAStaleSocket`: a regular file at the path is replaced, the listener accepts, the file mode is `0600` and the directory mode is `0700`.
    - `TestControlSocketPathFromEnv`: uses `t.Setenv`.
  - In `migration_control_test.go`, build the handler with `Options.Control` set and serve that mux on a socket in `t.TempDir()`. Then:
    - `TestControlPullProgressNeedsNoCookie`: `GET /api/migration/pull/progress` returns 200 when an admin exists and there is no cookie.
    - `TestControlCreatesATokenWithoutACookie`: `POST /api/migration/tokens {"lifetime":"24h"}` returns 201 with a token.
    - `TestWebTokensStillNeedTheCookie`: the same request on the TCP handler returns 401.
    - `TestControlConnectNeedsSelfURL`: no `selfUrl` gives 400.
    - `TestPanelStartsWhenTheControlSocketCannotBeCreated` (Review Focus 1): extract `main.serveControl(path string, mux http.Handler) (*http.Server, error)`. A path under a read-only directory returns an error, and `main` only logs it. Test the helper.
  - In `target_test.go`, `TestAbortDuringRetryLeavesAConsistentState` (Review Focus 2): pause a pull, call `Retry` and `Abort` concurrently, wait, and assert the phase is idle, `state.json` is gone, and `go test -race` is clean.
- [ ] **Step 2: Run them.** Command: `go test ./internal/migration/ ./internal/httpapi/ . -run 'Control|Stale|AbortDuringRetry|WebTokens'`. Expected: FAIL, undefined symbols.
- [ ] **Step 3: Implement the above.**
  - `main` creates `control := http.NewServeMux()` and passes it in `Options.Control`.
  - After `NewHandler`, call `serveControl(migration.ControlSocketPath(), control)`. On error, log `warning: no migration control socket: <err>` and go on.
  - Add the control server's `Shutdown` to Task 1's `shutdown(ctx)`. It is a no-op when the socket could not be created.
- [ ] **Step 4: Run.** Command: `go test -race ./internal/migration/ ./internal/httpapi/ && go test ./...`. Expected: ok.
- [ ] **Step 5: Commit.** `feat: serve the migration routes on a control socket inside the container, without the web's cookie`

### Task 3: What the old host is sending

**Files:**
- Modify: `internal/migration/items.go` (count bytes in `StreamItem`), `internal/migration/lock.go` (fields on `Source`)
- Modify: `internal/migration/source_http.go` (`GET /api/migration/serve/activity` in `RegisterControlRoutes` only)
- Test: `internal/migration/source_http_test.go`

**Interfaces:**
- Produces: `migration.Activity{Item string \`json:"item"\`; Bytes int64 \`json:"bytes"\`; Done int \`json:"done"\`}` and `(*Source).Activity() Activity`.

- [ ] **Step 1: Write the failing tests**
  - `TestActivityFollowsAStream`: while a fake item stream blocks halfway (a writer that signals after N bytes), `Activity()` returns the item's ID and `Bytes >= N`. After the stream ends, `Item == ""`, `Bytes == 0` and `Done == 1`.
  - `TestActivityIgnoresAFailedStream`: a stream that errors leaves `Done` unchanged.
  - `TestActivityRouteOnlyOnTheControlMux`: on the control mux it returns 200 with JSON. On the web mux it returns 404.
- [ ] **Step 2: Run them.** Command: `go test ./internal/migration/ -run Activity`. Expected: FAIL.
- [ ] **Step 3: Implement.** Wrap `out` in a counting writer that updates fields guarded by `s.mu`. Set `Item` at the start, and clear it at the end whatever the result.
- [ ] **Step 4: Run.** Command: `go test -race ./internal/migration/`. Expected: ok.
- [ ] **Step 5: Commit.** `feat: track which item the old host is sending and how far`

### Task 4: Typed client for the control socket

**Files:**
- Create: `internal/migrationctl/client.go`, `internal/migrationctl/client_test.go`

**Interfaces:**
- Consumes:
  - from Task 2: the control routes and `migration.SourceStatus`;
  - from Task 3: `migration.Activity`;
  - from the existing code: `migration.Progress`.
- Produces:
  - `migrationctl.New(socketPath string) *Client`, an `http.Client` whose transport dials the socket. Each request has a 30 s timeout, except `Start` and `Abort`, which get none.
  - Methods:
    - `Progress(ctx) (migration.Progress, error)`
    - `Connect(ctx, sourceURL, token, selfURL string) (migration.Progress, error)`
    - `Recheck(ctx, hostPaths []string) (migration.Progress, error)`
    - `Start(ctx, hostPaths []string) error`
    - `Retry(ctx) error`
    - `Abort(ctx) error`
    - `CreateToken(ctx, lifetime string) (Token, error)`
    - `RevokeToken(ctx) error`
    - `Status(ctx) (migration.SourceStatus, error)`
    - `Resume(ctx) (warning string, err error)`
    - `Activity(ctx) (migration.Activity, error)`
    - `Raw(ctx, method, path string, body []byte) (status int, respBody []byte, err error)`
  - `Token{Token string; ExpiresAt time.Time}`. The lifetimes are `"1h"`, `"24h"` and `"168h"`.
  - `*APIError{Status int; Message string}` for a non-2xx response with `{"error": ...}`.
  - `ErrNoPanel`, returned (wrapped) when dialing fails with `ENOENT` or `ECONNREFUSED`, or the connection drops with EOF or a reset. The TUI reads it as "not running, or restarting".
  - `Controller`, an interface with every method above except `Raw`. The TUI depends on it, and tests fake it.

- [ ] **Step 1: Write the failing tests.** Run an `httptest.NewUnstartedServer` with its `Listener` swapped for a Unix listener in `t.TempDir()`.
  - `TestClientDecodesProgress`
  - `TestClientReturnsAPIError`: a 409 with `{"error":"busy"}` gives `*APIError{409,"busy"}`.
  - `TestClientNoPanel`: no socket file gives `errors.Is(err, ErrNoPanel)`.
  - `TestClientDropIsNoPanel`: the handler hijacks the connection and closes it, which also gives `ErrNoPanel`.
  - `TestClientResumeWarning`: 200 with `{"warning":"x"}` gives `"x"`. 204 gives `""`.
- [ ] **Step 2: Run them.** Command: `go test ./internal/migrationctl/`. Expected: FAIL (no package).
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** Command: `go test ./internal/migrationctl/`. Expected: ok.
- [ ] **Step 5: Commit.** `feat: a client for the migration control socket`

### Task 5: The `pull` TUI

**Files:**
- Modify: `go.mod` and `go.sum` (`go 1.26.0`, plus the three Charm modules)
- Create: `internal/migrationtui/pull.go`, `internal/migrationtui/style.go` (shared lipgloss styles and the journey line), `internal/migrationtui/pull_test.go`

**Interfaces:**
- Consumes: `migrationctl.Controller` and `migrationctl.ErrNoPanel` from Task 4.
- Produces:
  - `migrationtui.NewPull(c migrationctl.Controller, defaultSelfURL string) tea.Model`
  - `migrationtui.QuitNote(m tea.Model) string`: the line printed after the program ends. For example: `The migration goes on. Run relist-panel migration pull to follow it again.`, or `""` when nothing was started.
  - The messages the model handles, exported for tests: `ProgressMsg{P migration.Progress; Err error}`, emitted by a 500 ms `tea.Tick` that calls `Progress`, and `ActionDoneMsg{Err error}`.

- [ ] **Step 1: Write the failing tests.** Drive `Update` with the messages, and assert on `View().Content` with ANSI stripped (`ansi.Strip` from `github.com/charmbracelet/x/ansi`). Use a `fakeController` that records calls.
  - `TestPullStartsOnConnectWhenIdle`: a `ProgressMsg` with phase `""` shows the fields `Old panel URL`, `Migration token` and `This panel's URL`. The last is prefilled with `defaultSelfURL`.
  - `TestPullRefusesABadURL`: entering `ftp://x` shows `enter an http:// or https:// URL` and makes no `Connect` call.
  - `TestPullPreviewDisablesStartWhileBlocked`: phase `connected` with a blocking check. The view shows the check's message, and `enter` does not call `Start`.
  - `TestPullPreviewShowsSizesVolumesAndImages`: phase `connected` with `info` holding:
    - a 2 GiB Volume `pgdata` with driver `local`;
    - a Volume `nfsdata` with driver options `type=nfs`;
    - one Image with `repoDigests` and one without.

    The view shows `Volumes`, `pgdata`, `2.0 GiB`, `nfsdata`, `type=nfs` (in the danger style), `1 pulled by digest` and `1 copied`. `r` calls `Recheck` with the current selection.
  - `TestPullPreviewLeavesSystemPathsUnticked`: `/srv/up` is shown ticked (`[x]`) and `/etc/localtime` unticked (`[ ]`). Space on a row calls `Recheck` with the new selection.
  - `TestPullStartNeedsY`:
    - `enter` shows `Start the migration? The old server stops every Compose and locks until you resume it. [y/N]`.
    - `n` returns to Preview without a call.
    - `y` calls `Start` with the selection.
  - `TestPullAbortNeedsY`: `a` shows `Abort? The old server resumes and starts its Composes again. Everything copied here is deleted. [y/N]`, and only `y` calls `Abort`.
  - `TestPullPausedOffersRetryAndAbort`: phase `paused` with an error. The view shows the error and `r retry`. `r` calls `Retry`.
  - `TestPullShowsBusyAsALine` (Review Focus 2): `Start` returns `*APIError{409, "another action is in progress"}`. The view shows that text, and the model does not quit.
  - `TestPullRestartingWhenThePanelIsGone` (Review Focus 4): during `finalizing`, a `ProgressMsg{Err: ErrNoPanel}` shows `Restarting the panel…`, keeps ticking, and does not quit. The next good `ProgressMsg` clears it.
  - `TestPullGivesUpAfterTwoMinutes`: `ErrNoPanel` messages spanning more than 2 minutes, through an injected clock (`NewPull` takes an unexported `now func() time.Time` via a test helper), show `the panel did not come back: run docker compose up -d in the install directory`.
  - `TestPullDoneListsDNSAndDeployments`: phase `done` with summary DNS `shop.example.com` and a Deployment `{ComposeID: 3, Number: 7}`. The view shows both and `Sign in with the admin account of the old server.`.
  - `TestPullFitsASmallWindow` (Review Focus 3): a `tea.WindowSizeMsg{Width: 80, Height: 24}` and phase `pulling` with 40 items. The content has at most 24 lines, the journey line and the key help are present, and `j`/`k` (or the down/up arrows) scroll the item list.
  - `TestPullJourneyHighlightsTheStage`: for each phase, the journey line marks the right stage among `Connect · Preview · Lock old server · Copy · Finalize · Done`.
- [ ] **Step 2: Run them.** Command: `go test ./internal/migrationtui/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - Use `textinput` (bubbles) for the three fields, with the token masked (`EchoPassword`). Use `progress` (bubbles) for item bars and `spinner` for waits. Render plain lists with lipgloss.
  - The view sets `AltScreen = true`.
  - Item row: kind, ID, bar, `formatBytes(done)/formatBytes(size)`, and state, which is `retrying n/3` when `Attempts > 1` and running.
  - The last 6 log lines sit under the items. `l` toggles a full-screen scrollable log (`viewport`).
  - `q` and ctrl+c quit.
- [ ] **Step 4: Run.** Command: `go test ./internal/migrationtui/ && go vet ./...`. Expected: ok.
- [ ] **Step 5: Commit.** `feat: the pull side of a migration as a Bubble Tea TUI`

### Task 6: The `serve` and `resume` TUIs

**Files:**
- Create: `internal/migrationtui/serve.go`, `internal/migrationtui/resume.go`, `internal/migrationtui/serve_test.go`

**Interfaces:**
- Consumes: from Task 4, `Controller`; from Task 5, the shared styles and `QuitNote`.
- Produces: `migrationtui.NewServe(c migrationctl.Controller) tea.Model` and `migrationtui.NewResume(c migrationctl.Controller) tea.Model`. `QuitNote` also covers both.

- [ ] **Step 1: Write the failing tests**
  - `TestServeOffersLifetimes`: with no lock and no token, the view lists `1 hour`, `24 hours` and `7 days`, with 24 hours selected. `enter` calls `CreateToken("24h")`.
  - `TestServeWarnsALiveTokenIsRevoked`: `Status` has `tokenExpiresAt` set. The view shows `Creating a token revokes the one that is live.`.
  - `TestServeShowsTheTokenOnce`: after `CreateToken`, the view shows the token, its expiry and `relist-panel migration pull`.
  - `TestServeFollowsTheLock`: `Status` steps `locked`, `stopping`, `maintenance` and `ready` tick the matching lines. `Activity{Item:"volume-a", Bytes: 2048, Done: 3}` shows `volume-a` and `3 items sent`.
  - `TestServeShowsMigrated`: step `migrated` with `targetUrl` shows `Migrated to https://new.example.com`.
  - `TestServeQuitBeforeClaimRevokes`: before a claim, `q` asks `Revoke the token? [Y/n]`. `enter` calls `RevokeToken` and quits. `n` quits without the call.
  - `TestServeCancelNeedsRESUME`: `c` before `migrated` asks to type `RESUME`. Typing `RESUM` plus `enter` makes no call. `RESUME` plus `enter` calls `Resume`.
  - `TestServeSkipsTheTokenWhenUnderWay`: a `Status` with step `stopping` goes straight to the follow view.
  - `TestResumeNeedsRESUME`: the same rule for `NewResume`. A returned warning is shown.
- [ ] **Step 2: Run them.** Command: `go test ./internal/migrationtui/ -run 'Serve|Resume'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** Command: `go test ./internal/migrationtui/`. Expected: ok.
- [ ] **Step 5: Commit.** `feat: the serve side and resume as Bubble Tea TUIs`

### Task 7: The `relist-panel migration` command

**Files:**
- Create: `internal/migrationcli/cli.go`, `internal/migrationcli/status.go`, `internal/migrationcli/cli_test.go`
- Modify: `main.go` (dispatch `migration` next to `reset-2fa`)

**Interfaces:**
- Consumes: from Task 4, `migrationctl.New`, `Client.Raw` and `ErrNoPanel`; from Tasks 5 and 6, the TUI constructors and `QuitNote`.
- Produces:
  - `migrationcli.Env{Stdin io.Reader; Stdout, Stderr io.Writer; IsTerminal func() bool; Socket string; Domain string; Run func(tea.Model, io.Reader, io.Writer) (tea.Model, error)}`
  - `migrationcli.DefaultEnv() Env`: `IsTerminal` is `term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())`, `Socket` is `migration.ControlSocketPath()`, `Domain` is `RELIST_DOMAIN`, and `Run` uses `tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out)).Run()`.
  - `migrationcli.Run(args []string, env Env) int`
  - `migrationcli.FormatStatus(p migration.Progress, s migration.SourceStatus, a migration.Activity) string`

- [ ] **Step 1: Write the failing tests**
  - `TestNeedsATerminal`: for `serve`, `pull` and `resume` with `IsTerminal` false, the exit code is 2 and stderr is the exact Global Constraints line.
  - `TestUnknownSubcommandPrintsUsage`: exit 2. The usage lists `serve`, `pull`, `status` and `resume`, and not `api`.
  - `TestNoPanel`: the socket is missing, so `status` exits 1 with `the panel is not running in this container (no /…/control.sock)`.
  - `TestStatusNewHost`: `FormatStatus` for phase `pulling`, 3 of 5 items done and the last log entry `Copied volume-a` contains these lines:
    - `Migration from https://old.example.com: pulling`
    - `Items: 3/5 done`
    - `Last: <hh:mm:ss> Copied volume-a`
  - `TestStatusOldHost`: idle progress and step `ready` with activity print `This server is locked for a migration: ready` and `Sending volume-a (2.0 KiB), 3 items sent`.
  - `TestStatusNone`: everything idle prints `No migration on this host.`.
  - `TestWatchWithNothingExitsOnce` (Review Focus 5): `status --watch` with everything idle prints `No migration on this host.` once and exits 0.
  - `TestWatchStopsAtDone`: a fake socket server returns `pulling`, then `done`. `--watch` (with an injectable interval set to 1 ms in tests) exits 0 after printing `done`.
  - `TestWatchSurvivesARestart`: the fake server drops one connection (as `ErrNoPanel`) between `finalizing` and `done`. The watch prints `Restarting the panel…` and still reaches `done`.
  - `TestAPIPrintsTheBody`: `api GET /api/migration/pull/progress` prints the JSON body and exits 0. A 409 response exits 1 and still prints the body.
  - `TestPullPassesTheDomainAsDefault`: with `Domain` `panel.example.com`, `Run` receives a pull model whose view prefills `https://panel.example.com`.
- [ ] **Step 2: Run them.** Command: `go test ./internal/migrationcli/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - In `main.go`: `if len(os.Args) > 1 && os.Args[1] == "migration" { os.Exit(migrationcli.Run(os.Args[2:], migrationcli.DefaultEnv())) }`.
  - After a TUI ends, print `QuitNote` to stdout when it is not empty.
- [ ] **Step 4: Run.** Command: `go test ./... && go build -o /dev/null .`. Expected: ok.
- [ ] **Step 5: Commit.** `feat: relist-panel migration serve, pull, status and resume`

### Task 8: Docs, the Docker page card, ADR-0019 and the sandbox

**Files:**
- Create: `docs/adr/0019-migration-driven-from-the-terminal.md`. It has the spec's Decision section with the rejected options, and amends ADR-0018: the web flow is to be replaced, and finalize re-executes instead of exiting.
- Modify: `docs/adr/0018-server-migration.md`. Add one line under the title: `Amended by ADR-0019: driven from the terminal; finalize re-executes the panel.`
- Modify: `CONTEXT.md` (add **Control socket** under Language, with the spec's wording)
- Modify: `README.md` (migration section, around lines 159–205): lead with the two commands, keep the web steps as the alternative, and add `migration status` and `migration resume`.
- Modify: `web/src/components/ServerMigrationCard.tsx` and its test: add a line `From a terminal: docker compose exec panel relist-panel migration serve`, using Typography `Text code copyable`. Read `llms.txt` and `docs/GUIDELINES.md` first.
- Modify: `internal/webassets/dist` (regenerate with `go generate ./internal/webassets/...`)
- Modify: `scripts/sandbox-migration.sh` (the ready printout gives the two `docker exec -it sandbox-* docker exec -it install-panel-1 relist-panel migration …` lines)

- [ ] **Step 1: Write the failing test.** In `ServerMigrationCard.test.tsx`, `shows the terminal command`: the text `docker compose exec panel relist-panel migration serve` is present.
- [ ] **Step 2: Run it.** Command: `pnpm --dir web exec vitest run src/components/ServerMigrationCard.test.tsx`. Expected: FAIL.
- [ ] **Step 3: Implement the card line, then the docs, ADR and sandbox printout.**
- [ ] **Step 4: Run.** Commands: `pnpm --dir web test && pnpm --dir web typecheck && go generate ./internal/webassets/... && go test ./...`. Expected: all pass.
- [ ] **Step 5: Commit, in two commits.**
  - `docs: record ADR-0019 and document the migration commands`, with the ADR, CONTEXT, README, card, test and sandbox.
  - `chore: rebuild the embedded frontend with the terminal command on the migration card`

### Task 9: Probe over the socket, and prove the re-execute

**Files:**
- Modify: `scripts/probe-migration.sh`, and the spec's Probe record (`.scratch/migration-tui/spec.md`)

- [ ] **Step 1: Change the probe's migrate section.**
  - Keep the web assertions for phase 1: setup is 409, and the pull routes give 401 without a sign-in.
  - Drive the new host with `ctl() { docker exec "$NEW" docker exec install-panel-1 relist-panel migration api "$@"; }`:
    - `ctl POST /api/migration/pull/connect '{"sourceUrl":…,"token":…,"selfUrl":"http://'$NEW_IP':8888"}'`
    - `ctl POST /api/migration/pull/start '{"hostPaths":["/srv/probe"]}'`
    - poll `ctl GET /api/migration/pull/progress`
- [ ] **Step 2: Add the re-execute checks.**
  - Before `start`, record the container's `StartedAt` and `RestartCount` (`docker exec "$NEW" docker inspect -f '{{.State.StartedAt}} {{.RestartCount}}' install-panel-1`).
  - Run `docker exec "$NEW" docker exec -d install-panel-1 sh -c 'relist-panel migration status --watch > /tmp/watch.log 2>&1'`.
  - After `done`:
    - `StartedAt` and `RestartCount` are unchanged.
    - `/tmp/watch.log` contains `Restarting the panel…` and ends with a line containing `done`.
    - `docker exec install-panel-1 ls -l /proc/1/fd` shows no `(deleted)` target.
  - Each failure calls `fail` with what it found.
- [ ] **Step 3: Run.** Command: `task probe:migration`. Expected: `probe: migration ok`.
- [ ] **Step 4: Record the run.** Append a dated line under the spec's Probe record, matching `.scratch/server-migration/spec.md`'s style. It covers the Docker version, that the container was not restarted, and that the watch survived.
- [ ] **Step 5: Commit.** `test: probe a migration driven over the control socket, and that finalize re-executes without restarting the container`

### Task 10: Manual check in the sandbox

Not automated (the spec's "The TUI itself").

- [ ] **Step 1:** Run `task sandbox:migration`. Then, in two terminals, follow the printout's `migration serve` (old) and `migration pull` (new). Go through Connect, Preview, Start, Copy and Finalize to Done. Check that the pull TUI stays up through "Restarting the panel…".
- [ ] **Step 2:** Run `task sandbox:migration:reset` and repeat. Quit `pull` mid-copy with `q`, run it again, and check it resumes the view with the log.
- [ ] **Step 3:** Ledger what was seen. Any defect goes back through TDD in the owning task's package.
