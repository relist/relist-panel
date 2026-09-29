# 07 — Volumes tab and Volume Explorer — the read-only slice

**What to build:** The tracer bullet. After this ticket an operator can open a Compose, see the
Volumes it mounts, open the Explorer on one, browse it, and download a file or a directory — end to
end. Every integration risk in the design lives at or before this point; everything after it is
ordinary work.

**Blocked by:** 05

**Status:** ready-for-agent

- [ ] The Compose page gains a third tab, Volumes, listing only that Compose's Volumes with kind, resolved source, target, and the container-side path each Service uses
- [ ] Before the first deploy the tab states why it is unavailable, carrying the CLI's `couldn't find env file`
- [ ] The Volume Explorer is its own route in the panel SPA, opened in a new browser tab, behind the panel's existing session auth — not a separate web app, and with no nav entry of its own since a Volume is meaningless without its Compose
- [ ] It lists a directory with name, size, mtime, owner uid/gid, mode and kind; navigates into directories; shows symlinks with their targets and refuses the ones that escape with a clear message
- [ ] A toolbar covers the read actions: refresh, download a file, download a directory as tar.gz. The slice is read-only — no write controls, no Monaco
- [ ] Resolved paths are shown (the host path for a bind source, `/var/lib/docker/volumes/<real>/_data` for a named Volume)
- [ ] A bind source outside the data directory gets a confirmation before its first operation, even though the operator declared it
- [ ] Document flow throughout, no nested scrollers (`docs/GUIDELINES.md` §6) — the file list runs long on the page
- [ ] The feature is unavailable, tab hidden, when the panel is not running in a container
- [ ] UI work happens directly in `web/`, per ADR-0009, with the annotated tag `design-mock` as the visual reference for the screens and states
