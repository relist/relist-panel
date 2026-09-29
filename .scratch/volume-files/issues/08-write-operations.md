# 08 — Write operations — upload, edit, delete (API and UI)

**What to build:** Everything that changes a Volume's contents, plus the warning that has to carry
the safety story for it.

**Blocked by:** 02, 03, 13, 05

**Status:** ready-for-agent

- [ ] Upload a file into the selected directory: 1 GiB cap, and refused before the stream starts when it exceeds the Volume's free space — the helper checks `statfs`. A full disk is a host-wide failure, not a panel one
- [ ] Edit a text file and save, with Monaco reused from the Compose YAML screen; a file above 1 MB is not opened in the editor and the Explorer says so
- [ ] Delete a file or a whole directory tree, permanent and confirmed by a `Popconfirm` naming the path (`docs/GUIDELINES.md` §6). There is no trash: a hidden trash directory inside the Volume would be visible to the application using it
- [ ] Every write is POST, PUT or DELETE — never GET — so the `SameSite=Lax` session cookie keeps them CSRF-safe
- [ ] When any Service in the Compose is running, the Explorer states that writing can corrupt a live stack, naming the consequence and the affected Services. The write is deliberately **not** gated behind an acknowledgement, so this banner is the only control here: it must name the consequence rather than say "be careful"
- [ ] Deliberately not in v1: create an empty file, mkdir, rename or move, copy/duplicate, a chmod/chown editor, search, previews, multi-select, drag-and-drop, symlink creation
- [ ] HTTP-seam tests with a faked runner asserting the argv for each write, and that the running-stack banner's condition is computed from the Compose's own status
