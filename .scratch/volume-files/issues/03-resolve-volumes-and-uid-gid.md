# 03 — Resolve a Compose's Volumes, uid/gid, and pre-flight

**What to build:** The panel's read-side knowledge of which Volumes a Compose mounts, what identity
an operation runs as, and whether each source is safe to mount. Source resolution goes through the
Compose CLI, so the panel never reimplements Compose's path or interpolation semantics.

**Blocked by:** None — can start immediately.

**Status:** ready-for-human

- [x] A Compose's Volumes are resolved with `docker compose -p <project> -f <file> --env-file <env> config --format json` and nothing else — no hand-rolled YAML parsing of paths
- [x] Both kinds come back: `type: volume` (logical name plus the real name from the top-level `volumes` map) and `type: bind` (the resolved absolute host path), each with `target`, `read_only`, and the Services that use it
- [x] Verified behaviour is preserved: relative binds resolve against the Compose file's directory, `${VAR}` interpolates from the generated env file, and volumes no Service references are pruned
- [x] When `config` fails — before the first deploy, `couldn't find env file` — the endpoint returns the CLI's own error and the Volumes tab is unavailable with that reason. No YAML fallback, and the env file is not written earlier (the panel uses the Compose file's presence as its "is deployed" signal)
- [x] A request can never supply a source path: the endpoint only ever returns what the CLI resolved. This is the invariant the whole feature's privilege rests on
- [x] UID/GID is the resolved `user:` for the Service the operator picks, from the same `config` output; otherwise `0:0` with a visible warning. The value is shown in the UI, override-able, and passed per operation as `--user`
- [x] Pre-flight, because a `docker run` with a missing source creates it: a named volume is checked with `docker volume inspect` before the first operation and again before `archive`/`extract`; a bind source inside the data directory is `os.Stat`-ed before every operation and refused if absent; a bind source outside it cannot be checked, and the confirmation states that continuing creates it `root:root 0755`
- [x] HTTP-seam tests with a faked Compose CLI, covering a named Volume with a `name:` override, a relative bind, an absolute bind, `${VAR}` interpolation, both rungs of the UID/GID rule, and every failure path above

## Comments

Landed without endpoints — ticket 03's tests land at the CLI port seam, since no HTTP
surface exists yet; the endpoint-level (HTTP-seam) cases are ticket 05's.

`internal/volumes` gains the read side: `Volume` (kind, logical name, resolved source,
and one `Use` per Service with target + read-only), `ServiceUser` (the resolved
`user:` or the `0:0` fallback with its warning and origin), the two-method `CLI` port
(`ComposeConfig`, `VolumeExists`), and the `Resolver` over it plus the data directory.
Parsing reads only `services.*.volumes[]` and the top-level `volumes` map; anonymous
volumes and `tmpfs` are skipped, and output the real CLI would not produce (a relative
bind, a named volume with no top-level real name, a mount with no target) is an error
rather than a guess. `Preflight` models the three outcomes explicitly — `ok`,
`missing` (refused), `unverifiable` (the root:root 0755 confirmation) — checking named
volumes with `VolumeExists` and binds inside the data directory with `os.Stat`.
`internal/dockerexec/volume_resolver.go` implements the port: the spec's exact
`docker compose ... config --format json` argv with the CLI's stderr folded into the
error (`couldn't find env file` included), and `docker volume inspect <name>` mapped
to a boolean so only a real "no such volume" reads as missing. No `main.go` or
`httpapi` wiring — tickets 05/08/12 own that.

Evidence: `gofmt -l . && go vet ./... && go test ./...` under the project's toolchain
(`docker run --rm -v "$PWD":/src ... golang:1.27-alpine`) — gofmt silent, vet clean,
full suite green. New tests fake the CLI: canned `config --format json` output with a
named volume's top-level real name, a relative bind resolved absolute, an interpolated
`${VAR}` path, one source shared by two Services, and both UID/GID rungs; malformed
output and a `couldn't find env file` failure both surface as errors; every pre-flight
outcome, with the volume check faked and `t.TempDir()` as the data directory. The CLI's
resolution behaviour was verified directly against real Docker 29 (relative bind →
the Compose file's directory, `${VAR}` from the env file, unreferenced volumes pruned).

**Post-review:** a Service's resolved `user:` must itself be a numeric `uid:gid`. Compose accepts
names (`postgres`) and bare uids, and `--user` would resolve either against the helper image's
`/etc/passwd`, not the Service's own — so the API answers 400 and the Explorer explains, asking for
the explicit numeric override. The spec's UID/GID section was amended in the same change.
