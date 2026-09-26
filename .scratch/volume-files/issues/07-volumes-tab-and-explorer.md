# 07 — Volumes tab and Volume Explorer: the read-only slice

**What to build:** The tracer bullet. After this ticket an operator can open a Compose, see the
Volumes it mounts, start a handle, browse one, and download a file or a directory — end to end. Every
integration risk in the design lives at or before this point; everything after it is ordinary work.

**Blocked by:** 01, 03, 05, 06

**Status:** needs-triage

- [ ] The Compose page gains a third tab, Volumes, listing only that Compose's Volumes with kind, source, target, and the container-side path each Service uses
- [ ] Before the first deploy the tab states why it is unavailable, carrying the CLI's `couldn't find env file`
- [ ] Start opens a handle; the tab shows its state, the resolved uid/gid with an override, and read-only vs read-write
- [ ] A bind source outside the data directory gets a confirmation before its first handle, even though the operator declared it
- [ ] The Volume Explorer is its own route in the panel SPA, opened in a new browser tab, behind the panel's existing session auth — not a separate web app, and with no nav entry of its own since a Volume is meaningless without its Compose
- [ ] It lists a directory with name, size, mtime, owner uid/gid, mode and kind; navigates into directories; shows symlinks with their targets and refuses the ones that escape with a clear message; and downloads a file or a directory as tar.gz or zip
- [ ] Resolved paths are shown (the host path for a bind source, `/var/lib/docker/volumes/<real>/_data` for a named Volume)
- [ ] Document flow throughout, no nested scrollers (`web-design/GUIDELINES.md` §6) — the file list runs long on the page
- [ ] Implementation follows the approved design from ticket 01, screen by screen, including the states it renders — the mock is the visual contract, not a sketch to be reinterpreted
- [ ] No write operations and no Monaco on this ticket; the slice is read-only, so it needs no caps and its security story is already closed
