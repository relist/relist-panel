# 11 — Delete

**What to build:** An operator can fully tear down a Compose — stopping and removing its containers, networks, and volumes, and deleting its files from disk.

**Blocked by:** 08

**Status:** ready-for-agent

- [ ] `dockerexec.Down` with volumes (`-v`) is implemented for real and added to the fake `ComposeRunner`
- [ ] Clicking "Delete" (per `ComposeGeneral.dc.html`) prompts for confirmation, then removes containers/networks/volumes and deletes the Compose's row and its on-disk files
- [ ] Covered by HTTP-seam tests using the fake `ComposeRunner`
