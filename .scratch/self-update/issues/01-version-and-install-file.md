# 01 — Version stamp, image-based Install file, embedded copy

Status: closed

Spec: [../spec.md](../spec.md) — "Install file and image pin".

## Do

- Add `var version = "dev"` in `main.go`. Set it with `-ldflags -X main.version=…` in
  the `Dockerfile`, from a `VERSION` build arg that defaults to `dev`, and in
  `Taskfile.yml`. Log it at startup.
- Add `version` to the response of `GET /api/auth/status`. This is the only
  unauthenticated route that shows it; `/healthz` stays as it is.
- In `docker-compose.yml`:
  - the panel Service keeps `build: .`;
  - it gains `image: ghcr.io/relist/relist-panel:local` and `pull_policy: build`. The tag is never
    `latest`, which the published image will use;
  - add the comment header the spec describes.
- Add `docker-compose.override.example.yml`, a commented example: one extra label and
  one extra volume.
- Embed `docker-compose.yml` in the binary. A small `internal/install` package exports
  `EmbeddedInstallFile() []byte`.
- Test that the embedded bytes equal the repo file. This catches a build that forgot to
  re-embed it.

## Watch out

The live install runs from this checkout. `docker compose up -d --build` keeps working
unchanged. After the first rebuild, the old `relist-panel-panel` image is left dangling;
prune it or leave it.
