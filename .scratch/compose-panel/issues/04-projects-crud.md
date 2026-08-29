# 04 — Projects: CRUD + app shell

**What to build:** A logged-in operator sees the app's navigation shell and can create, list, edit, and delete Projects.

**Blocked by:** 03

**Status:** ready-for-agent

- [ ] A migration creates the `projects` table (id, name, description, created_at) per `docs/sa.md`'s Data Dictionary
- [ ] The app shell (Sider + Menu, per `Main.dc.html`) renders on every authenticated page from this ticket onward
- [ ] The Projects list page shows all Projects as cards (name, description, environment count, last-updated)
- [ ] An operator can create a Project (name + description)
- [ ] An operator can edit a Project's name/description
- [ ] An operator can delete a Project
- [ ] Covered by HTTP-seam tests per the spec's Testing Decisions
