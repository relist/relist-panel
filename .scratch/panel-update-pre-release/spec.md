# Panel update to a pre-release — spec

Status: ready-for-agent

## Goal

The owner can move a test Install to a release candidate (`X.Y.Z-rc.N`) with one
command, and can move it on to the next candidate or stable release the same way.
Pre-releases are not offered anywhere else. Stable Installs, the web banner and
`releases/latest/download/install.sh` see only stable releases, as they do today.

Out of scope: a channel setting in the web, finding the newest pre-release
automatically, and any pre-release form other than `-rc.N`.

## Background

Four things stop a pre-release today:

1. `DefaultManifestURL` is `releases/latest/download/update-manifest.json`, and
   GitHub never resolves `latest` to a pre-release.
2. `parseVersion` (`internal/panelupdate/version.go`) accepts only
   `MAJOR.MINOR.PATCH`. `ParseManifest` rejects any other version, and a build
   stamped with any other version is treated as a development build that never updates.
3. `semver.greater` has no pre-release ordering.
4. The release workflow rejects any tag that is not `vX.Y.Z`, publishes every
   release as a full release, and moves `:latest` every time.

`install.sh --update` already passes `RELIST_UPDATE_MANIFEST_URL` from the
shell into the update Container (`release/install.sh`, `do_update`). The
manifest signature, Preflight, Apply and rollback need no change.

## Design

### 1. Versions

- A release version is `MAJOR.MINOR.PATCH` or `MAJOR.MINOR.PATCH-rc.N`, with an
  optional leading `v`. Each of the four numbers is a decimal integer with no
  leading zeros (`0`, or a number that does not start with `0`). Nothing
  else is a release version. For example, `1.2.3-rc1`, `1.2.3-beta.1` and
  `1.2.3-rc.1+x` are rejected.
- Order follows semver precedence: `0.1.0-rc.1 < 0.1.0-rc.2 < 0.1.0-rc.10 <
  0.1.0 < 0.1.1-rc.1 < 0.1.1`. N is compared as a number.
- `parseVersion` and `greater` in `internal/panelupdate/version.go` implement
  this. Every caller (`ParseManifest`, `Checker`, `Service.Development`,
  `Available`, `best`) gets the new rules without other changes.
- A build stamped `0.1.0-rc.1` is a release build. Its web banner keeps
  checking the default (stable) URL and offers only a stable release greater than
  itself. For example, it offers `0.1.0` and never `0.0.2`.

### 2. `install.sh --update --version VERSION`

```bash
curl -fsSL https://github.com/relist/relist-panel/releases/latest/download/install.sh | sudo sh -s -- --update --version 0.1.0-rc.1
```

- `--version VERSION` is valid only with `--update`. Without `--update` it is a
  usage error (exit 2). A fresh install of a candidate uses that candidate's own
  `install.sh` (`releases/download/vX.Y.Z-rc.N/install.sh`).
- VERSION must match the grammar in §1. A leading `v` is stripped. Anything else
  is a usage error.
- The script sets
  `RELIST_UPDATE_MANIFEST_URL=https://github.com/relist/relist-panel/releases/download/v<VERSION>/update-manifest.json`
  for the update Container. From there the run is the same as `--update`.
- `--version` together with a non-empty `RELIST_UPDATE_MANIFEST_URL` in the
  environment is a usage error, so it is never unclear which one wins.
- `--version` works for stable versions too, for example `--version 0.0.3`.
- The Updater never moves an Install backwards. A `--version` lower than or equal
  to the running version reports "up to date", as today.
- `usage()` and the README's install options table list the flag.

### 3. Release workflow (`.github/workflows/release.yml`)

- The tag check accepts `^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$`. The step
  outputs `prerelease=true|false`.
- The image is pushed as `ghcr.io/relist/relist-panel:<version>`, rc versions
  included, and `VERSION` is stamped as before.
- `gh release create` gets `--prerelease` when `prerelease == 'true'`.
- The "Tag the image latest" step runs only when `prerelease != 'true'`.
- The manifest, the signature, `install.sh` and the verify step stay the same.
  `relist-release verify` calls `ParseManifest`, so it accepts rc versions once §1
  is done.
- `defaultNotesFmt` (`releases/tag/v%s`) already gives the right notes URL for an
  rc tag.

### 4. Rollout order

The update Container runs the **current** image, so an Install accepts an rc
manifest only if its running version already has §1. Release 0.0.1 does not.
There are two ways in:

- Release a stable version with this change first (for example 0.0.2). After
  that, any Install on 0.0.2 or later can use `--update --version X.Y.Z-rc.N`.
- Or install the candidate fresh on a test host with that candidate's `install.sh`.

The owner's live Install builds from source and is never updated (service-image
invariant). Candidates are tested on a separate Install that runs the GHCR image.

### 5. Docs

- New ADR `docs/adr/0026-release-candidates.md`. It covers the version grammar,
  rc releases that are GitHub pre-releases and never `:latest`, and opt-in by
  `install.sh --update --version` only. It amends ADR-0024's release-version
  rule. Considered and rejected: a web channel setting, and auto-discovery
  through the GitHub API.
- README, Panel update section: how to move a test Install to a candidate and
  back to stable, and the "never backwards" rule.
- `CONTEXT.md`: add **Release candidate** if the glossary has release terms.

## Testing

- `internal/panelupdate`: a table test of `parseVersion` covering valid and
  invalid forms, including `1.2.3-rc1` → invalid and `1.2.3-rc.1` → valid. Also
  a table test of the ordering chain in §1, both directions, plus equal versions.
  Then a `Checker` test where running `0.1.0-rc.1` with manifest `0.1.0` is not
  up to date, and running `0.1.0` with manifest `0.1.0-rc.2` is up to date.
- `cmd/relist-release`: `manifest --version 0.1.0-rc.1` followed by `verify`
  passes.
- `install_script_test.go`:
  - `--version` without `--update` → exit 2.
  - A bad VERSION → exit 2.
  - `--version` plus `RELIST_UPDATE_MANIFEST_URL` → exit 2.
  - A valid `--version` puts the expected URL into the update Container's
    environment. The test stubs `docker` the way the existing tests do; if no
    such stub exists, test the URL-building function on its own.
- `shellcheck -s sh release/install.sh` and `actionlint` (Docker images
  `koalaman/shellcheck`, `rhysd/actionlint`).
- `go vet ./...` and `go test ./...` are green.

## Constraints

- No schema migration, no Install file change, no web change.
- Never touch the live install: no `docker compose` in the repo root, nothing
  under `/var/lib/relist-panel`.
- Commits: Conventional Commits, no scope, no trailers. Never push unasked.
