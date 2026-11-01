# Panel update — follow-ups after the branch

Left open on purpose. The final whole-branch review triaged these as able to wait.

## Before the first release tag

- Ticket 02's owner steps: see `issues/02-release-workflow-and-key.md` → Comments.
  The pinned action SHAs were checked on 2026-11-01. The workflow runs on
  `workflow_dispatch` only until the tag trigger is put back.
- The live install on the dev host runs from this checkout. Before its next
  `docker compose up -d`, copy `docker-compose.override.example.yml` to
  `docker-compose.override.yml` (the build block), or it runs `latest` from GHCR. The
  Containers are renamed once, to `relist-panel` and `relist-traefik`.
- `shellcheck -s sh release/install.sh` and `actionlint` were not available where the
  one-updater rework was built; the release workflow runs `shellcheck` on the filled
  script, and `actionlint` has not run on the workflow.

## Can wait

- Web
  - `splitUnifiedDiff` drops the unchanged gaps between hunks, so the Monaco line
    numbers are wrong for multi-hunk diffs.
  - The page polls forever if the run disappears after a restart.
  - A succeeded run whose version differs from the target shows nothing.
  - The terminal-run alert comes back on every visit.
  - No test covers the AppShell tag or the menu entry.
  - `revalidateOnReconnect` is left at its default.
- Lock
  - A narrow TOCTOU between the update lock's unlock and a Migration start.
  - The metrics sampler and the vuln daily scheduler still write while locked; the
    README documents this.
  - `settle` logs on every pass if `UpdaterState` keeps failing.
- Updater
  - `healthyOnce` hides the last real error when the context expires.
  - The `ImageID` ref goes into the URL unescaped. The ref is signed.
  - A failed `docker run -d` can leave a `created` Updater container. The next Apply
    starts a new run id, so it is not blocked; remove the old one with `docker rm -f`.
- Preflight
  - Cancelling the context replaces a Ready report.
  - A queued preflight waits on `pfRun` and ignores its context.
  - The `service-image` invariant's own image field is not compared with the
    manifest's image.
  - The blame wording does not match the doc comment.
- Discovery
  - A `:` or `,` in the `working_dir` label would break the `-v` spec of the update
    Container. The labels come from the panel's own container.
- Update check
  - `s.mu` is held across the 30 s fetch.
  - `best()` reads the store twice.
  - The settings body has no `MaxBytesReader`.
  - The small-order key tests have no order-8 vector.
- CLI
  - The usage test does not cover help/version.
  - Use `slices.Contains` instead of the nested loop.
  - The usage column width is fixed.
- Auth status
  - It returns `map[string]any`, which could be a typed struct.
  - The embed test is a weak guard.
- Release
  - The manual steps do not mention that `RELIST_ADMIN_*` is no longer read.
- Probe
  - The rollback leg does not verify a real `panel.db` restore; unit tests cover it.
