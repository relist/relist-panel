# 08 — Write operations

**What to build:** Everything that changes a Volume's contents, plus the warning that has to carry
the whole safety story for it.

**Blocked by:** 01, 05

**Status:** needs-triage

- [ ] Upload a file into the selected directory; create an empty text file; edit a text file and save; create a directory; rename or move an entry; delete a file or a whole directory tree
- [ ] Every write is POST, PUT or DELETE — never GET — so the `SameSite=Lax` session cookie keeps them CSRF-safe
- [ ] The text editor is Monaco, reused from the Compose YAML screen rather than a second editor dependency
- [ ] Delete is permanent and confirmed by a `Popconfirm` naming the path (`GUIDELINES.md` §6). There is no trash: a hidden trash directory inside the Volume would be visible to the application using it
- [ ] Renaming or deleting a symlink acts on the link, not on its target
- [ ] When any Service in the Compose is running, the Explorer states that writing can corrupt a live stack and names the affected Services. The write is deliberately **not** gated behind an acknowledgement, so this banner is the only control here: it must name the consequence rather than say "be careful"
- [ ] Deliberately absent, per the spec's v1 operation set: copy/duplicate, a chmod/chown editor, search, previews, multi-select bulk operations, drag-and-drop move, symlink creation
- [ ] HTTP-seam tests with a faked exec asserting the argv for each write, and that the running-stack banner's condition is computed from the Compose's own status
