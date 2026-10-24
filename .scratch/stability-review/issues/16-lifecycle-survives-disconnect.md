# 16 — Stop, Restart, Delete and teardown finish when the client goes away

**What to build:** these handlers run `docker compose` under `r.Context()`; `exec.CommandContext`
kills the CLI when the browser tab closes. Verified on real Docker: killing `down -v` three seconds
in left three stopped-but-not-removed containers, the volume and the network, with nobody shown an
error. Run the Docker work under a context detached from the request, with its own timeout, as
prune already does in `internal/dockerhost/api.go`.

**Blocked by:** none

**Status:** ready-for-agent

- [ ] Exported helper in `internal/composes`: `LifecycleContext(parent context.Context) (context.Context, context.CancelFunc)`
      = `context.WithTimeout(context.WithoutCancel(parent), 10*time.Minute)`
- [ ] Used by Compose stop, restart, delete, and by Project/Environment delete around teardown
- [ ] HTTP test: canceling the request context while the fake runner's `DownWithVolumes` is running
      does not cancel the runner's context, and the Compose row is deleted when it returns

## Source

Stability review, item C4 (`.scratch/stability-review/spec.md`). Added when planning on
2026-10-24; it was verified in the review but missed in the first ticket split.
