# 13 — The `docker run --rm` runner seam and its test fake

**What to build:** The one seam that turns an operation into a container run. It builds the
per-operation argv from the spec's Run spec and runs it, and it installs the test fake that records
argv — so every HTTP-seam test downstream can assert exactly what would run, with no Docker in tests.

**Blocked by:** None — can start immediately.

**Status:** ready-for-human

- [x] A runner seam that takes an operation, a resolved source, a uid/gid and a read/write choice, and produces the spec's argv: `docker run --rm -i --network none --user <uid>:<gid> --read-only --tmpfs /tmp --cap-drop ALL --security-opt no-new-privileges -v <source>:/data[:ro] --entrypoint /usr/local/bin/relist-volhelper <image> <op> -- <relpath>`
- [x] `:ro` for read operations and `:rw` for write operations, chosen at the mount flag — the kernel is the enforcer, never the UI; exactly one mount, never the Docker socket, never another host path
- [x] Resolves the panel's image name with `docker inspect $(hostname)` → `.Config.Image`, falling back to `RELIST_IMAGE`; no registry, no runtime pull
- [x] A fake installs in place of the runner for tests: it records the argv it was asked to run and feeds scripted output back, so HTTP-seam tests assert exact argv with no Docker in them
- [x] Cancelling the operator's HTTP request kills the `docker` CLI child the panel started; the helper's pipe closes and it dies on `EPIPE`/`EOF`, and `--rm` removes the container
- [x] The cheapest first landing: no UI, no Docker in tests, so the runner and its fake can land while everything else is still open

## Comments

Landed. `internal/volumes` declares the `Op` wire strings, the `Request`, and the
`Runner` port; `internal/dockerexec/volume_runner.go` builds the spec's argv per
op (`:ro` mounts for `list`/`stat`/`read`/`archive`, read-write for
`write`/`delete`/`extract`, `archive --format tar.gz`, `--` before every path),
runs it as argv with stdin/stdout wired and stderr folded into the error, and
resolves the image via `docker inspect <hostname>` → `.Config.Image` with the
`RELIST_IMAGE` fallback. `internal/volumes/faketest.Runner` records each call's
`Request` and stdin and scripts stdout/error. Nothing is wired into `main.go` or
`httpapi` yet — downstream tickets do that.

Evidence: `gofmt -l . && go vet ./... && go test ./...` under the project's
toolchain (`docker run --rm -v "$PWD":/src -v relist-gomod:/go/pkg/mod -v
relist-gocache:/root/.cache/go-build -w /src golang:1.27-alpine sh -c ...`) —
gofmt silent, vet clean, full suite passes. New tests cover exact argv for every
op, `:ro`/`:rw` by op, user and source passthrough, a path starting with `-`,
streaming through the docker seam, stderr/cancellation surfacing, and both image
resolution paths.
