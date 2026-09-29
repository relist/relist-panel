# 03 — Resolve a Compose's Volumes, uid/gid, and pre-flight

**What to build:** The panel's read-side knowledge of which Volumes a Compose mounts, what identity
an operation runs as, and whether each source is safe to mount. Source resolution goes through the
Compose CLI, so the panel never reimplements Compose's path or interpolation semantics.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] A Compose's Volumes are resolved with `docker compose -p <project> -f <file> --env-file <env> config --format json` and nothing else — no hand-rolled YAML parsing of paths
- [ ] Both kinds come back: `type: volume` (logical name plus the real name from the top-level `volumes` map) and `type: bind` (the resolved absolute host path), each with `target`, `read_only`, and the Services that use it
- [ ] Verified behaviour is preserved: relative binds resolve against the Compose file's directory, `${VAR}` interpolates from the generated env file, and volumes no Service references are pruned
- [ ] When `config` fails — before the first deploy, `couldn't find env file` — the endpoint returns the CLI's own error and the Volumes tab is unavailable with that reason. No YAML fallback, and the env file is not written earlier (the panel uses the Compose file's presence as its "is deployed" signal)
- [ ] A request can never supply a source path: the endpoint only ever returns what the CLI resolved. This is the invariant the whole feature's privilege rests on
- [ ] UID/GID is the resolved `user:` for the Service the operator picks, from the same `config` output; otherwise `0:0` with a visible warning. The value is shown in the UI, override-able, and passed per operation as `--user`
- [ ] Pre-flight, because a `docker run` with a missing source creates it: a named volume is checked with `docker volume inspect` before the first operation and again before `archive`/`extract`; a bind source inside the data directory is `os.Stat`-ed before every operation and refused if absent; a bind source outside it cannot be checked, and the confirmation states that continuing creates it `root:root 0755`
- [ ] HTTP-seam tests with a faked Compose CLI, covering a named Volume with a `name:` override, a relative bind, an absolute bind, `${VAR}` interpolation, both rungs of the UID/GID rule, and every failure path above
