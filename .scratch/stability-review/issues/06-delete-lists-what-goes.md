# 06 — Delete confirmation lists exactly what will be removed

**What to build:** the Compose Delete DangerModal names the named volumes and the directories
(including relative bind mounts like `./data` inside the Compose directory) that will be removed,
and separately the shared volumes that will be kept (ticket 03). This is the one place a modal
earns its place: it is the action that destroys data.

**Blocked by:** 03

**Status:** ready-for-agent

- [ ] A read endpoint (or a field on an existing one) returns the removal plan computed the same
      way as ticket 03: volumes to remove, volumes kept and why, the Compose directory
- [ ] The modal shows the plan before the typed confirmation; the request still uses the server's
      own plan at execution time
- [ ] Project and Environment deletion show the same plan summed over their Composes
- [ ] HTTP test for the plan; component test for the modal

## Source

Stability review, item B4; discussion on 2026-10-24.
