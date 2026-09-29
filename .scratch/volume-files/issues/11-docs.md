# 11 — Docs: README and ADR cross-references

**What to build:** The operator-facing documentation this feature changes, and the cross-references
to the ADRs whose boundaries it moved.

**Blocked by:** 07, 08, 12

**Status:** ready-for-agent

- [ ] README documents `RELIST_IMAGE`, alongside the existing env vars
- [ ] README states that the feature requires a containerized panel — it needs the panel image on the host and the Docker socket — and that the Volumes tab is hidden otherwise
- [ ] README states that this feature assumes TLS on the panel's domain: a volume file manager over plain HTTP is a data-exfiltration channel
- [ ] README does not document `RELIST_VOLUME_IDLE_TIMEOUT` — the re-plan removed that variable, and the docs must not resurrect it
- [ ] ADR-0002 (the panel drives the host via the `docker compose` CLI) and ADR-0003 (exactly one port/adapter pair in the whole system) each gain a pointer to ADR-0008 and ADR-0010, since both boundaries moved
