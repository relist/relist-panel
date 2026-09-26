# 03 — Resolve a Compose's Volumes, and the UID/GID chain

**What to build:** The panel's read-side knowledge of which Volumes a Compose mounts, and what
identity a handle over one should run as. Source resolution goes through the Compose CLI so the
panel never reimplements Compose's path or interpolation semantics.

**Blocked by:** 01

**Status:** needs-triage

- [ ] A Compose's Volumes are resolved with `docker compose -p <project> -f <file> --env-file <env> config --format json` and nothing else — no hand-rolled YAML parsing of paths
- [ ] Both kinds come back: `type: volume` (logical name plus the real name from the top-level `volumes` map) and `type: bind` (the resolved absolute host path), each with `target` and `read_only`
- [ ] Verified behaviour is preserved: relative binds resolve against the Compose file's directory, `${VAR}` interpolates from the generated env file, and volumes no Service references are pruned
- [ ] When `config` fails — before the first deploy, `couldn't find env file` — the endpoint returns the CLI's own error and the Volumes tab is unavailable with that reason. No YAML fallback, and the env file is not written earlier (the panel uses the Compose file's presence as its "is deployed" signal)
- [ ] A request can never supply a source path: the endpoint only ever returns what the CLI resolved. This is the invariant the whole feature's privilege rests on
- [ ] UID/GID resolution chain, first hit wins: resolved `user:` from `docker compose config` → `docker inspect` on the Service's container `.Config.User` → `stat -c '%u:%g' /data` from a root helper run → `0:0` (opt-in only, with a warning)
- [ ] UID/GID never comes from exec'ing into the target container: `docker inspect` needs no `exec` in the target, works on distroless and scratch images, works when the container is stopped, and cannot be blocked by the target's own security policy
- [ ] Pre-flight existence check: a named Volume is refused unless `docker volume inspect` finds it; a bind source under the data directory is stat-ed directly; a bind source outside it is reported as unverifiable, so the UI can warn that continuing creates it `root:root 0755`
- [ ] HTTP-seam tests with a faked Compose CLI, covering a named Volume with a `name:` override, a relative bind, an absolute bind, `${VAR}` interpolation, each rung of the UID/GID chain, and every failure path above
