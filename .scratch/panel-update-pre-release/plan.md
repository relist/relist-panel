# Panel update to a release candidate — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the owner move a test Install to a release candidate (`X.Y.Z-rc.N`) with
`install.sh --update --version X.Y.Z-rc.N`, and let the release workflow publish such
candidates as GitHub pre-releases that never become `:latest`.

**Architecture:** `internal/panelupdate/version.go` learns the `-rc.N` grammar and semver
ordering. Every caller picks that up without changes. `release/install.sh` gets a
`--version` flag that turns into `RELIST_UPDATE_MANIFEST_URL`, which the update path already
passes to the update Container. `.github/workflows/release.yml` accepts rc tags, marks them
`--prerelease` and skips the `:latest` step for them.

**Tech Stack:** Go (stdlib `regexp`, `strconv`), POSIX `sh`, GitHub Actions YAML.

**Spec:** `.scratch/panel-update-pre-release/spec.md` (binding). Background:
`docs/adr/0024-panel-self-update.md` and `docs/adr/0025-one-updater-and-install-script.md`.

## Global Constraints

- The release version grammar is `MAJOR.MINOR.PATCH` or `MAJOR.MINOR.PATCH-rc.N`, with an optional leading `v`. Each number is a decimal integer. Nothing else is a release version.
- The order is `0.1.0-rc.1 < 0.1.0-rc.2 < 0.1.0-rc.10 < 0.1.0 < 0.1.1-rc.1 < 0.1.1`. N is compared as a number.
- The rc manifest URL is `https://github.com/relist/relist-panel/releases/download/v<VERSION>/update-manifest.json`.
- The workflow tag regex is `^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$`.
- Change no schema migration, no `docker-compose.yml` (Install file) and nothing under `web/`.
- Never touch the live install. Do not run `docker compose` in the repo root, and write nothing under `/var/lib/relist-panel`. Never run the `relist-panel` binary without a temp `RELIST_DATA_DIR`.
- Commits use Conventional Commits with no scope, imperative mood and lowercase after the colon. **Add no `Co-Authored-By` or `Claude-Session` trailers.** Never push.
- Done means `go vet ./...` and `go test ./...` are green, `shellcheck -s sh release/install.sh` passes (`docker run --rm -v "$PWD:/mnt" -w /mnt koalaman/shellcheck:stable -s sh release/install.sh`) and actionlint passes (`docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:latest`).

## Review Focus

1. **A trailing newline or embedded newline in `--version`** (for example `0.1.0\nEVIL=1`) must be a usage error. It must never end up in the URL or in a `-e` argument. This goes in the Task 2 bad-version table.
2. **A running rc that sees a stable manifest of the same core** (running `0.1.0-rc.2`, manifest `0.1.0`) must be offered the update. A manifest of the same rc must be up to date. This goes in Task 1's `TestCheckReleaseCandidates`.
3. **`rc.10` vs `rc.9`** must compare as numbers, not strings. This goes in Task 1's ordering test.
4. **`--version=0.1.0-rc.1` (the `=` form)** must work like the other flags' `=` forms. This goes in Task 2's URL test.
5. **A stable tag still moves `:latest`** after the workflow change. The `:latest` step's `if:` must be false only for rc tags. This goes in Task 3's workflow test.

---

### Task 1: Release-candidate versions in `panelupdate`

**Files:**
- Modify: `internal/panelupdate/version.go` (whole file)
- Test: `internal/panelupdate/service_test.go` (`TestVersionParsing`, around line 415)
- Test: `internal/panelupdate/checker_test.go` (new test after `TestCheckUpToDate`)
- Test: `cmd/relist-release/main_test.go` (`env.manifest` helper and a new round-trip test)

**Interfaces:**
- Produces: `parseVersion(s string) (semver, bool)` and `(a semver) greater(b semver) bool`, the same names and signatures as today. `semver` becomes a struct: `type semver struct { core [3]int; pre bool; rc int }`. `pre` is false for a stable version. Nothing outside `version.go` reads its fields.

- [ ] **Step 1: Write the failing tests**

In `service_test.go`, extend the `TestVersionParsing` map so that these are valid:
`"1.2.3-rc.1"`, `"v1.2.3-rc.10"` and `"0.0.1-rc.0"`.
These must be invalid: `"1.2.3-rc1"` (kept), `"1.2.3-rc."`, `"1.2.3-beta.1"`, `"1.2.3-rc.1+x"`, `"1.2.3-rc.-1"` and `"1.2.3\n"`.

Add `TestVersionOrder` in the same file:

```go
func TestVersionOrder(t *testing.T) {
	chain := []string{"0.1.0-rc.1", "0.1.0-rc.2", "0.1.0-rc.9", "0.1.0-rc.10", "0.1.0", "0.1.1-rc.1", "0.1.1", "1.0.0-rc.1"}
	for i := range chain {
		for j := range chain {
			a, b := mustVersion(chain[i]), mustVersion(chain[j])
			if got, want := a.greater(b), i > j; got != want {
				t.Errorf("%s > %s = %v, want %v", chain[i], chain[j], got, want)
			}
		}
	}
}
```

The `i == j` pairs cover "equal is not greater".

In `checker_test.go`, add `TestCheckReleaseCandidates`. It uses `newCRig` with `crigOpts{current: embeddedFile, theirs: newerFile, version: ..., manifestV: ...}`:

- running `0.1.0-rc.2` with manifest `0.1.0`: `rep.UpToDate == false` and `rep.Failed == nil`
- running `0.1.0` with manifest `0.1.0-rc.2`: `rep.UpToDate == true`
- running `0.1.0-rc.2` with manifest `0.1.0-rc.2`: `rep.UpToDate == true`
- running `0.0.2` with manifest `0.1.0-rc.1`: `rep.UpToDate == false` and `rep.Failed == nil`

In `cmd/relist-release/main_test.go`, change `env.manifest(t)` to `env.manifestAt(t, version string)` and keep `manifest(t)` as `manifestAt(t, "1.4.0")`. Add `TestManifestSignVerifyReleaseCandidate`. It runs `manifestAt(t, "0.1.0-rc.1")`, then `sign`, then `verify`, and expects no errors. It parses the result and asserts `m.Version == "0.1.0-rc.1"` and `m.NotesURL == "https://github.com/relist/relist-panel/releases/tag/v0.1.0-rc.1"`.

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `go test ./internal/panelupdate ./cmd/relist-release -run 'TestVersion|TestCheckReleaseCandidates|TestManifestSignVerifyReleaseCandidate'`
Expected: FAIL. The rc strings parse as invalid, `ParseManifest` rejects the rc manifest, and `verify` fails.

- [ ] **Step 3: Implement the grammar and the order in `version.go`**

- Pattern: `^v?(\d+)\.(\d+)\.(\d+)(?:-rc\.(\d+))?$`. Go's `$` without the `m` flag matches only at the end of the text, so `"1.2.3\n"` stays invalid. The test pins this.
- `greater`: compare `core` first. If the cores are equal, a stable version (`!pre`) is greater than an rc. If both are rcs, compare `rc` as integers.
- Update the doc comment on `parseVersion` so it names both forms.

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go test ./internal/panelupdate ./cmd/relist-release`
Expected: PASS, with every existing test still green.

- [ ] **Step 5: Commit**

```bash
git add internal/panelupdate/version.go internal/panelupdate/service_test.go internal/panelupdate/checker_test.go cmd/relist-release/main_test.go
git commit -m "feat: accept release candidate versions in panel update"
```

---

### Task 2: `install.sh --update --version`

**Files:**
- Modify: `release/install.sh`. Touch `usage()`, `parse_args()`, `validate_args()` and the start of `do_update()`.
- Modify: `README.md`, the install options table (around line 76)
- Test: `install_script_test.go`

**Interfaces:**
- Consumes: nothing from Task 1. The script checks the grammar itself, and the panel checks it again.
- Produces: the shell variable `VERSION_ARG`, which is empty unless `--version` was given. When it is set, `validate_args` sets `RELIST_UPDATE_MANIFEST_URL`. The existing `${RELIST_UPDATE_MANIFEST_URL:-}` block in `do_update` passes it on unchanged.

Name the variable `VERSION_ARG`, not `VERSION`. `VERSION` is the release's own version, filled in at `@VERSION@`.

- [ ] **Step 1: Write the failing tests**

Add a `"--version"` check to `TestInstallScriptHelp`: the help output must contain it.

Add `TestInstallScriptVersionUsageErrors`. Each case must get `code == 2` and `docker == ""`.

- `{"--version", "0.1.0-rc.1"}` without `--update`. stderr contains `--version needs --update`.
- With `--update`, the bad versions `"0.1.0-rc1"`, `"0.1"`, `"latest"`, `"0.1.0-beta.1"`, `"0.1.0\nEVIL=1"`, `"0.1.0 "` and `""`. stderr contains `--version takes a version like 0.1.0 or 0.1.0-rc.1`.
- `{"--update", "--version", "0.1.0-rc.1"}` with env `RELIST_UPDATE_MANIFEST_URL=http://x/m.json`. stderr contains `either --version or RELIST_UPDATE_MANIFEST_URL, not both`.
- `{"--update", "--version"}` (missing value). Only the exit code is checked.

Add `TestInstallScriptVersionPrintsTheManifestURL`. Run it with each of `{"--update", "--version", "0.1.0-rc.1"}`, `{"--update", "--version", "v0.1.0-rc.1"}` and `{"--update", "--version=0.1.0-rc.1"}`. The script is not run as root here, so it stops at `check_root`. Assert that stdout contains `Update manifest: https://github.com/relist/relist-panel/releases/download/v0.1.0-rc.1/update-manifest.json`. Also assert that `--update` without `--version` prints no `Update manifest:` line.

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `go test . -run 'TestInstallScript'`
Expected: FAIL. You get `unknown option: --version`, and help does not list the flag.

- [ ] **Step 3: Implement the flag in `release/install.sh`**

- In `usage()`, add the line `  --version VERSION    with --update: update to this release, like 0.1.0-rc.1`.
- In `parse_args`, start `VERSION_ARG=""` and set `VERSION_SET=0`. Add `--version` to the `--domain | --email | ...` value list, and add a `--version=*` case. Both set `VERSION_ARG` and `VERSION_SET=1`.
- In `validate_args`, when `VERSION_SET` is 1:
  - Without `--update` → `usage_error "--version needs --update"`.
  - Strip one leading `v`. Reject newlines the way `valid_domain` does. Check the result against `grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$'`. A failure → `usage_error "--version takes a version like 0.1.0 or 0.1.0-rc.1"`.
  - If `RELIST_UPDATE_MANIFEST_URL` is non-empty → `usage_error "Give either --version or RELIST_UPDATE_MANIFEST_URL, not both"`.
  - Set `RELIST_UPDATE_MANIFEST_URL="https://github.com/relist/relist-panel/releases/download/v$VERSION_ARG/update-manifest.json"`.
- Make `say "Update manifest: $RELIST_UPDATE_MANIFEST_URL"` the first line of `do_update`, before `check_root`, and only when `VERSION_SET` is 1.
- README options table: add the row `| `--version VERSION` | with `--update`: update to this release, like `0.1.0-rc.1` |`.

- [ ] **Step 4: Run the tests and shellcheck, and confirm they pass**

Run: `go test . -run 'TestInstallScript'`
Expected: PASS under both `sh` and `dash`.

Run: `docker run --rm -v "$PWD:/mnt" -w /mnt koalaman/shellcheck:stable -s sh release/install.sh`
Expected: no output, exit 0.

- [ ] **Step 5: Commit**

```bash
git add release/install.sh install_script_test.go README.md
git commit -m "feat: add install.sh --version to update to a given release"
```

---

### Task 3: Release workflow publishes rc tags as pre-releases

**Files:**
- Modify: `.github/workflows/release.yml`. Touch the "Read the version and the public key" step, "Create the GitHub Release" and "Tag the image latest".
- Test: `cmd/relist-release/main_test.go`. Touch `workflowStep` (line 234) and the new test.

**Interfaces:**
- Produces: the step output `steps.meta.outputs.prerelease`, which is `true` or `false`.

- [ ] **Step 1: Write the failing test**

Add `If string \`yaml:"if"\`` to `workflowStep`. Add `TestReleaseWorkflowReleaseCandidates`:

- The meta step's `Run` contains `^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$` and `prerelease=`.
- The `gh release create` step's `Run` contains `--prerelease`.
- The step whose `Run` contains `:latest` has `If == "steps.meta.outputs.prerelease != 'true'"`.

`TestReleaseWorkflowHandlesTheKeyAndLatestCarefully` must still pass unchanged.

- [ ] **Step 2: Run the test and confirm it fails**

Run: `go test ./cmd/relist-release -run TestReleaseWorkflow`
Expected: `TestReleaseWorkflowReleaseCandidates` fails.

- [ ] **Step 3: Edit `release.yml`**

- **meta step:**
  - Use the new tag regex.
  - Change the error text to `tag $TAG is not vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-rc.N`.
  - Write `prerelease=true` to `$GITHUB_OUTPUT` when `$TAG` matches `-rc\.`, and `prerelease=false` otherwise.
- **Create the GitHub Release:**
  - Pass `PRERELEASE: ${{ steps.meta.outputs.prerelease }}` through `env`.
  - In the script, set `flags=""` and then `[ "$PRERELEASE" = true ] && flags=--prerelease`.
  - Add `$flags` unquoted to `gh release create`, with a `# shellcheck disable=SC2086` comment.
- **Tag the image latest:** add `if: steps.meta.outputs.prerelease != 'true'`.
- Header comment: add one line saying that rc tags are published as pre-releases and never move `:latest` (ADR-0026).

- [ ] **Step 4: Run the tests and actionlint, and confirm they pass**

Run: `go test ./cmd/relist-release`
Expected: PASS.

Run: `docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:latest`
Expected: no output, exit 0. actionlint runs shellcheck on `run:` blocks.

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/release.yml cmd/relist-release/main_test.go
git commit -m "feat: publish rc tags as pre-releases that never move latest"
```

---

### Task 4: ADR, README and glossary

**Files:**
- Create: `docs/adr/0026-release-candidates.md`
- Modify: `README.md`, the "Panel update" section (around line 243)
- Modify: `CONTEXT.md`. Add an entry next to **Update manifest** (around line 146).

- [ ] **Step 1: Write ADR-0026** in the same style as ADR-0025. Use a title, a problem paragraph, `**Decision:**` with bullets, and `## Considered options`.
  - **Decision:**
    - The release version grammar and order from Global Constraints.
    - rc tags are GitHub pre-releases, so `releases/latest/...` and the web banner never see them.
    - rc tags never move `:latest`.
    - The only way onto a candidate is `install.sh --update --version`.
    - A Panel update never moves backwards.
    - An Install accepts an rc only if its running version already knows the grammar: 0.0.1 does not.
    - The decision amends ADR-0024's "Versions come from a signed Update manifest" bullet: the release version is no longer only `MAJOR.MINOR.PATCH`.
  - **Considered options:**
    - A stable/beta channel setting in the web. Not now: only the owner tests candidates.
    - Finding the newest pre-release through the GitHub API. Rejected: rate limits, and a test Install could move to a candidate nobody picked.
    - Full semver pre-release identifiers. Rejected under YAGNI.

- [ ] **Step 2: README, Panel update section.** Add a short "Release candidates" paragraph after the shell update command:
  - the `--update --version 0.1.0-rc.1` command
  - going back with `--update` alone, which moves on only once a stable release is newer
  - the 0.0.1 limit
  - that a source-build Install never updates

- [ ] **Step 3: CONTEXT.md.** Add this entry:

  **Release candidate**: A panel version `X.Y.Z-rc.N`, published as a GitHub pre-release and reached only with `install.sh --update --version` (ADR-0026). _Avoid_: Beta, Pre-release channel

- [ ] **Step 4: Check**

Run: `go test ./...` and `go vet ./...`
Expected: PASS. No code changed in this task, so this confirms the branch is green.

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0026-release-candidates.md README.md CONTEXT.md
git commit -m "docs: record release candidates in adr-0026, readme and glossary"
```
