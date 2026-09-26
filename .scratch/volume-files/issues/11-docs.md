# 11 — Docs: README and ADR cross-references

**What to build:** The operator-facing documentation this feature changes, and the cross-references
to the ADR whose boundaries it moved. The design-side docs (`web-design/README.md`,
`GUIDELINES.md`) are ticket 01's job, not this one.

**Blocked by:** 01, 07

**Status:** needs-triage

- [ ] README documents `RELIST_VOLUME_IDLE_TIMEOUT` and `RELIST_IMAGE`, alongside the existing env vars
- [ ] README states that the feature requires a containerized panel — it needs the panel image on the host and the Docker socket — and that the Volumes tab is hidden otherwise
- [ ] README states that this feature assumes TLS on the panel's domain: a volume file manager over plain HTTP is a data-exfiltration channel
- [ ] `web-design/GUIDELINES.md` still matches what was actually built, if implementation diverged from the ticket-01 design at any point
- [ ] ADR-0002 (the panel drives the host via the `docker compose` CLI) and ADR-0003 (exactly one port/adapter pair) each gain a pointer to ADR-0008, since both boundaries moved
