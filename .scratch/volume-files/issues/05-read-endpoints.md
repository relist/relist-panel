# 05 — Read endpoints and backup download, one run per operation

**What to build:** The HTTP surface the Volume Explorer reads through. Each request is one
`docker run --rm` invoking the helper: list, stat and read, plus a backup download that streams
tar.gz straight from the helper's stdout into the HTTP response.

**Blocked by:** 02, 03, 13

**Status:** ready-for-agent

- [x] `GET .../composes/{composeID}/volumes` serves the resolved Volumes, each pre-flight status, every Service's uid/gid resolution and the feature-availability flag (session 1/3)
- [ ] Endpoints for list, stat and read, each one `docker run --rm` with the path as an argv element after `--`
- [ ] `read` streams raw bytes straight into the response with `io.Copy`: nothing buffered whole in memory, nothing written to disk
- [ ] `GET` backup streams the helper's tar.gz stdout into the response with no temp file anywhere, so a 50 GB Volume never becomes a 50 GB file in the data directory; a single file is served as a plain download, not wrapped in an archive
- [ ] `Content-Disposition` follows RFC 6266: control characters stripped, and `filename*` percent-encoded for any non-ASCII name
- [ ] Paths are always relative to the Volume root, and a path from a request is never interpolated into a shell string — there is no shell in the path from request to helper
- [ ] A refusal from the helper (escaping path, missing entry) surfaces as the panel's normal JSON error carrying the helper's own message, not a generic failure
- [ ] HTTP-seam tests fake the runner and assert the exact argv for each operation, including a path beginning with `-` and a path containing a newline

## Comments

**session 1/3:** the first HTTP surface landed — `GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/volumes`
resolves one Compose's Volumes and Services through `volumes.Resolver` (one CLI call, no caching),
runs `Preflight` per Volume, reports every Service's uid/gid with the `0:0` fallback warning, and
answers `available`/`reason` from the runner's new `Available` probe (image resolution; a bare binary
or a missing socket says the panel must run in a container). The not-deployed CLI error
(`couldn't find env file`) surfaces as a 502 body carrying the CLI's own message; an unknown
Compose — or Project/Environment — is a 404. `internal/volumeapi` owns the route;
`composes.Service.ComposeRunRequest` supplies the deploy identity so no SQL or path derivation is
duplicated, and `faketest.CLI` joins `faketest.Runner` as the HTTP-seam doubles.

Evidence: `gofmt -l . && go vet ./... && go test ./...` under `golang:1.27-alpine` — gofmt silent,
vet clean, full suite green. New HTTP-seam tests script the faked CLI and runner and assert the full
200 shape (named volume + three binds, a source shared by two Services, preflight ok/missing/
unverifiable, compose user and root fallback), `available: false` with its reason (and no CLI call),
the not-deployed error, and the parent-aware 404s; the runner's `Available` is unit-tested on both
paths.
