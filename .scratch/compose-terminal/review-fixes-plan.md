# Compose terminal — review fixes plan

> **For agentic workers:** Execute task by task, test-first where a task names a test. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Fix the findings of the two-axis review of `feat-terminal` (`main...HEAD` at `1089374`), so the branch matches its spec and the repo's UI contract.

**Spec:** [spec.md](spec.md). **Original plan:** [plan.md](plan.md); its Global Constraints still apply.

## Global Constraints

- Branch `feat-terminal`. Commits: Conventional Commits, no scope, **no** `Co-Authored-By` / `Claude-Session` trailers (AGENTS.md).
- Before editing `web/`: read `https://ant.design/llms.txt` and the pages for the components touched, and `docs/GUIDELINES.md`. antd is 6.5.1.
- Do not change behaviour the review confirmed correct: the foreign-container refusal, the Origin check, the single `:tab?` route, the cap of 10, the resize worker, the wrapper `Cmd`, and the PID line hiding.
- Gate before every commit: `go test -race ./internal/terminal/... ./internal/dockerexec/...` for Go changes, and `pnpm --dir web test && pnpm --dir web typecheck` for web changes. Run the full `go test -race ./...` once at the end (the `httpapi` package takes ~4 min).

## Decisions (already made — do not reopen)

| Finding | Decision |
| --- | --- |
| Paste may arrive twice | Do not read the clipboard ourselves. On a paste chord, return `false` from the custom key handler and let the browser's native `paste` event reach xterm, which then pastes exactly once. This also needs no clipboard permission. |
| Exit code may read 0 too early | One shared helper polls `GET /exec/{id}/json` until `Running` is false and `ExitCode` is non-null. It is used by `ExitCode`, `HasShell` and `Kill`. |
| Sweep only sends HUP and stops on the first error | The sweep uses the same hang-up as the session (HUP → grace → probe `0` → KILL), one goroutine per orphan. `OrphanShells` returns what it found plus a joined error instead of aborting. |
| Output lost on Reconnect after a redeploy | Once a terminal box has been created it stays mounted until the page unmounts. "Nothing running" after a Reconnect shows as an inline `Alert` above the kept box. |
| Cap taken before resolving | Keep the code: refusing the 11th session before any Docker call is better. Amend the spec's Sequence to match. |
| ADR-0012 text vs code | Update the ADR to describe HUP → grace → `kill -0` probe → KILL, for both the session and the sweep. |
| `gap={12}`, `padding: 8` | `gap={8}`. `padding: token.paddingXS`. |
| `type="primary"` outside Deploy/Create/Save (GUIDELINES §2) | Remove `type="primary"` from "Open the Editor tab", "Connect" and "Sign in" in `ComposeTerminalTab.tsx`. |
| "Task 7" in code comments | Reword to name the thing ("the startup sweep"). |
| GUIDELINES §7 missing the component | Add a `ComposeTerminalTab` row. |
| Not fixed (judgement calls) | The `composeRequest` duplication with `volumeapi`, string-typed shell/user, the `Picks` clump, and the lower-case "bash is not installed…" (spec copy; `bash` is a command name). |

---

### Task 1: Paste once

**Files:** `web/src/data/terminal-session.ts`, `web/src/data/terminal-session.test.ts`, `web/src/components/ComposeTerminalTab.tsx` (~lines 245–275)

**Produces:** `export function terminalKeyAction(event: Pick<KeyboardEvent, 'type' | 'key' | 'ctrlKey' | 'shiftKey' | 'metaKey'>): 'copy' | 'paste' | 'pass'`. It returns `copy` for Ctrl+Shift+C and Cmd+C (meta without ctrl), `paste` for Ctrl+Shift+V and Cmd+V, and `pass` otherwise, including keyup and a plain Ctrl+C (which must stay SIGINT). Key matching is case-insensitive.

- [ ] Write tests covering those cases, including `{key:'c', ctrlKey:true}` → `pass` and `{key:'V', ctrlKey:true, shiftKey:true}` → `paste`.
- [ ] Run `pnpm --dir web test -- terminal-session`. Expect FAIL.
- [ ] Implement the function. In the component, the custom key handler uses it as follows:
  - `copy` with a selection → write it to the clipboard and return `false`.
  - `copy` without a selection → return `true`.
  - `paste` → return `false` and do nothing else; no `navigator.clipboard.readText`, no `term.paste`.
  - `pass` → return `true`.
- [ ] Run the web gate. Expect PASS.
- [ ] Commit: `fix: paste into the terminal once`

### Task 2: Wait for Docker's exit code

**Files:** `internal/dockerexec/terminal.go` (`ExitCode` ~508, `HasShell` ~300–335, `Kill`), `internal/dockerexec/terminal_test.go`

**Produces:** an unexported `func (e *TerminalExec) waitExec(ctx context.Context, execID string) (int, error)`. It decodes `{"Running": bool, "ExitCode": *int}` and polls every 50 ms while `Running` is true or `ExitCode` is null, until `ctx` ends. A context end returns an error naming the exec path and "exit code not reported". `ExitCode`, `HasShell` and `Kill` all use it.

- [ ] Tests (fake socket server scripts the inspect answers):
  - `TestTerminalExecExitCodeWaitsForTheDaemon`: inspect answers `{"Running":true,"ExitCode":null}` twice, then `{"Running":false,"ExitCode":3}`. `ExitCode` returns `3`.
  - `TestTerminalExecExitCodeGivesUpWithTheContext`: inspect always answers `Running:true`; a 200 ms context makes `ExitCode` return an error containing `exit code not reported`.
  - `TestTerminalExecHasShellWaitsForTheDaemon`: a `null` code first, then `0`, gives `true`. A `null` code first, then `127`, gives `false`.
- [ ] Run `go test ./internal/dockerexec/ -run 'WaitsFor|GivesUp' -v`. Expect FAIL.
- [ ] Implement.
- [ ] In `internal/terminal/session.go`, when `ExitCode` fails after EOF, send `{"type":"error","message":"The shell ended, but Docker did not report its exit code"}` instead of `exit` with `0`. Add `TestTerminalSessionReportsAnUnknownExitCode` to `internal/httpapi/terminal_test.go`: the fake's `ExitCodeErr` is set, and the client receives that `error`. Add `ExitCodeErr error` to `faketest.Session`/`Exec` if it is not there.
- [ ] Run the Go gate. Expect PASS.
- [ ] Commit: `fix: wait for docker to report a terminal's exit code`

### Task 3: Sweep hangs up like a session, and survives one bad exec

**Files:** `internal/terminal/sweep.go`, `internal/terminal/sweep_test.go`, `internal/terminal/session.go` (the hang-up), `internal/dockerexec/terminal.go` (`OrphanShells` ~130–160), `internal/dockerexec/terminal_test.go`, `main.go` (the `Sweep` call)

**Produces:**
- `func hangUp(ctx context.Context, exec Exec, containerID string, pid int, grace time.Duration) error` in package `terminal`, extracted from the session's cleanup. The session calls it unchanged in behaviour.
- `func Sweep(ctx context.Context, exec Exec, projects []string, grace time.Duration, logf func(string, ...any))`. `main.go` passes `2 * time.Second`.

- [ ] Tests:
  - `TestSweepKillsAnOrphanThatIgnoresHUP`: the fake has no `KillExits[pid]`. The non-`"0"` kills are `HUP` then `KILL`.
  - `TestSweepStopsAtHUPWhenItWorks`: `KillExits[pid] = true`. The only non-`"0"` kill is `HUP`.
  - `TestSweepHangsUpOrphansConcurrently`: three orphans with `grace = 200ms` finish in under 500 ms.
  - `TestSweepHangsUpWhatItFoundDespiteAListingError`: `OrphanShells` returns one orphan plus an error. That orphan is hung up and the error is logged.
  - In `dockerexec`, `TestTerminalExecOrphanShellsContinuesPastAFailedInspect`: two containers, where the first container's exec inspect answers 500. The second container's marked exec is still returned, and the error is non-nil and names the failing path.
- [ ] Run `go test ./internal/terminal/ ./internal/dockerexec/ -run 'Sweep|OrphanShells' -v`. Expect FAIL.
- [ ] Implement:
  - `OrphanShells` collects per-container and per-exec errors with `errors.Join` and keeps going. A 404 (vanished) is still skipped silently.
  - `Sweep` hangs up everything returned even when the error is non-nil, with one goroutine per orphan and a `sync.WaitGroup`.
- [ ] Run the Go gate. Expect PASS.
- [ ] Commit: `fix: kill orphaned terminal shells that ignore a hang-up`

### Task 4: Keep the output across a Reconnect

**Files:** `web/src/components/ComposeTerminalTab.tsx` (`running` ~149, the xterm effect ~280–295, the render ~470–500)

- [ ] Change these:
  - Add `const [boxCreated, setBoxCreated] = useState(false)`, set `true` the first time the xterm instance is created.
  - The xterm effect depends on `boxCreated || running`, never on `running` alone, so a later `running === false` does not dispose it.
  - When `boxCreated && !running`, render the toolbar disabled. Above the box, show `<Alert type="info" title="Deploy to start a terminal" description={…same copy as the EmptyState…} action={<Button onClick={onOpenEditor}>Open the Editor tab</Button>} />`, and keep the box with its output.
  - The `EmptyState` is kept only for `!boxCreated && !running`.
- [ ] Manual check (dev server against a real Compose):
  1. Connect and print some output.
  2. Stop the Compose, which gives "Connection lost".
  3. Press Reconnect. The old output is still visible and the info `Alert` shows.
  4. Start the Compose and press Reconnect. A new session opens below the divider.
- [ ] Run the web gate. Expect PASS.
- [ ] Commit: `fix: keep terminal output when a reconnect finds nothing running`

### Task 5: UI contract

**Files:** `web/src/components/ComposeTerminalTab.tsx` (:485, :495, :562, :596, :623), `internal/terminal/session.go` (:44, :184), `docs/GUIDELINES.md` §7

- [ ] Change these:
  - `gap={12}` → `gap={8}`.
  - `padding: 8` → `padding: token.paddingXS`.
  - Remove `type="primary"` from the three buttons named in Decisions.
  - Replace "Task 7's sweep" with "the startup sweep" in both comments.
  - In §7's table, add `| ComposeTerminalTab | components/ComposeTerminalTab.tsx | The Compose screen's Terminal tab body: Service/Container Select, Shell and User Segmented, Connect, and the xterm box at LAYOUT.terminalHeight. One session per page; output survives a lost connection. |`.
- [ ] Run the web gate and `go vet ./internal/terminal/`. Expect PASS.
- [ ] Commit: `fix: hold the terminal tab to the ui guidelines`

### Task 6: Docs match the code

**Files:** `docs/adr/0012-*.md`, `.scratch/compose-terminal/spec.md`

- [ ] ADR-0012: describe cleanup as HUP → grace (2 s) → `kill -0` probe → KILL only if still alive, both on session end and in the startup sweep. Also record that the sweep keeps going past a failed inspect.
- [ ] spec.md, §Sequence step 2: reorder it to "Server accepts, checks the session cap, resolves the Container, probes the shell, and starts the exec". Add one sentence of why: a refused session costs no Docker calls.
- [ ] Commit: `docs: describe terminal cleanup as built`

### Task 7: Final verification

- [ ] `go test -race ./...` passes.
- [ ] `pnpm --dir web test && pnpm --dir web typecheck` pass.
- [ ] `task build` succeeds. If `internal/webassets/dist` changed, commit `chore: rebuild the embedded frontend with the terminal fixes`.
- [ ] Report: commits made, test output, and which manual checks were or were not run, with the reason.
