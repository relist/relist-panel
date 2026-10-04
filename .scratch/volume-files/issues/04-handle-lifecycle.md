# 04 — Handle lifecycle and the container run spec

**What to build:** Creating, stopping, and reconciling the helper container that a Volume Handle is
made of — the whole lifetime of a container the panel owns outside any Compose project.

**Blocked by:** 01, 02

**Status:** wontfix

- [ ] Start runs the documented `docker run` with `--network none`, `--cap-drop ALL`, `--security-opt no-new-privileges`, `--read-only`, `--tmpfs /tmp`, exactly one `-v <source>:/data`, and an explicit `--user`
- [ ] The mount is `:ro` unless the handle was explicitly opened for writing — enforced at the mount flag, never in the UI
- [ ] Every handle container carries `relist.handle` and `relist.compose` labels
- [ ] The panel resolves its own image with `docker inspect $(hostname)` → `.Config.Image`, falling back to `RELIST_IMAGE`. The host already has that image from `docker compose build`, so there is no registry and no runtime pull
- [ ] Panel start stops every container carrying the `relist.handle` label; nothing is adopted. Reason: a handle from the previous build could be asked to run an operation its older helper does not implement, and that bug would only ever appear during an upgrade
- [ ] Idle auto-stop: default 30 minutes measured from that handle's last HTTP request, `RELIST_VOLUME_IDLE_TIMEOUT` overrides it, `0` disables it. Closing a browser tab does not count as idle — the panel has no websocket by scope, so a closed tab is indistinguishable from an idle one
- [ ] One handle per Volume: opening a Volume that already has a live handle focuses that handle instead of creating a second
- [ ] Deleting a Compose stops its handles before `DownWithVolumes`, so `docker compose down -v` cannot fail or leave a Volume behind because a helper still holds it
- [ ] A Compose edit that removes a Volume stops that Volume's handle, since its source no longer appears in the resolved list
- [ ] Starting a handle is refused, and the Volumes tab hidden, when the panel is not running in a container — the feature needs both the panel image on the host and the Docker socket
- [ ] The panel never mounts the Docker socket or any other host path into a handle container
- [ ] HTTP-seam tests with a faked Docker CLI: start, stop, idle expiry, the startup sweep, one-handle-per-volume, and the Compose-delete ordering

## Comments

Closed `wontfix` by the re-plan (ADR-0010): the new model has no persistent container, so
there is no lifecycle to build. The part of this ticket that survives — the runner seam —
moved to ticket 13.
