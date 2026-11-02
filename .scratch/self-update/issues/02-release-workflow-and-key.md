# 02 — Release workflow and signing key

Status: ready-for-human
Blocked by: 01, 03

Spec: [../spec.md](../spec.md) — "Releases", "Update manifest".

## Human

- Generate the ed25519 release key and keep the private half offline.
- Add the private half as the repo secret `RELIST_RELEASE_KEY` (base64).
- Replace the placeholder public key constant in `internal/panelupdate/key.go`
  (created by ticket 03) with the real public half.
- After the workflow's first push, open the package `ghcr.io/relist/relist-panel` on GitHub.
  Check that it is linked to the repository and that its visibility is **Public**; set it
  if not. A host pulls without logging in, so a private package fails every Panel update.

## Agent (once the secret exists)

- Add `.github/workflows/release.yml`, run on push of tag `v*`:
  - buildx for amd64 and arm64, with `VERSION` set from the tag;
  - push `ghcr.io/relist/relist-panel:<version>` and `ghcr.io/relist/relist-panel:latest` to GHCR, then read the
    index digest. Log in with `GITHUB_TOKEN`; the job needs `permissions: packages: write`;
  - run `go run ./cmd/relist-release manifest` to write `update-manifest.json`. It
    takes the version, digest, `docker-compose.yml`, `release/invariants.json` and
    `release/manual-steps.json`;
  - run `go run ./cmd/relist-release sign` with the secret to write the `.sig`;
  - create the GitHub Release with both files attached.
- Add `cmd/relist-release` (manifest and sign) and a `verify` subcommand for local
  checks.
- Add `release/invariants.json` and `release/manual-steps.json`, edited by hand for each
  release.

## Comments

The code and the workflow are done; what remains is for the owner:

- Run `go run ./cmd/relist-release keygen`.
- Store the private key as the repo secret `RELIST_RELEASE_KEY`.
- Commit the public key in `internal/panelupdate/key.go`.
- After the first push of a release, check the GHCR package is Public and linked to the repo.
- ~~Verify the pinned action SHAs in `.github/workflows/release.yml`.~~ Checked
  2026-11-01 with `git ls-remote`: all six match their tags.
- Enable the workflow: it runs on `workflow_dispatch` only for now. Put back the
  `push: tags: ["v*"]` trigger written in its header comment.
