# 11 — Docs: README and ADR cross-references

**What to build:** The operator-facing documentation this feature changes, and the cross-references
to the ADRs whose boundaries it moved.

**Blocked by:** 07, 08, 12

**Status:** ready-for-human

- [x] README documents `RELIST_IMAGE`, alongside the existing env vars
- [x] README states that the feature requires a containerized panel — it needs the panel image on the host and the Docker socket — and that the Volumes tab is hidden otherwise
- [x] README states that this feature assumes TLS on the panel's domain: a volume file manager over plain HTTP is a data-exfiltration channel
- [x] README does not document `RELIST_VOLUME_IDLE_TIMEOUT` — the re-plan removed that variable, and the docs must not resurrect it
- [x] ADR-0002 (the panel drives the host via the `docker compose` CLI) and ADR-0003 (exactly one port/adapter pair in the whole system) each gain a pointer to ADR-0008 and ADR-0010, since both boundaries moved

## Comments

**session 1/1:** README: `RELIST_IMAGE` joins the env list — the panel image that
Volume file operations run from, set only when `docker inspect $(hostname)` cannot
name it, wired in through the panel service's `environment:` since nothing passes it
through by default, with the image required on the host. A new `## Volume files`
section states the containerized-panel requirement (image on the host + mounted
Docker socket; the bare binary hides the Volumes tab) and the TLS assumption (a
volume file manager over plain HTTP is a data-exfiltration channel). No
`RELIST_VOLUME_IDLE_TIMEOUT` anywhere. ADR-0002 and ADR-0003 each gain one forward
pointer to ADR-0008/ADR-0010 — the plain-`docker run` containers outside Compose, and
the second thing worth faking in tests — the boundaries this feature moved. No ADR
index exists to update. `git diff --check` clean; README and both ADRs re-read after
the edits.
