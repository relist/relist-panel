# 07 — Composes: CRUD of YAML

**What to build:** Within an Environment, an operator can create and edit Composes by writing/pasting a `docker-compose.yml` in a Monaco editor. Composes are listed but not yet deployable — that's ticket 08.

**Blocked by:** 05

**Status:** ready-for-agent

- [ ] A migration creates the `composes` table (id, environment_id FK, name, yaml_content, created_at, updated_at), unique on (environment_id, name), per `docs/sa.md`
- [ ] The Composes sub-tab (per `ProjectDetail.dc.html`) lists all Composes in the active Environment, with status shown as a placeholder (e.g. "not deployed") since deploy execution doesn't exist yet
- [ ] An operator can create a Compose with a name and YAML content
- [ ] The Compose detail page's General tab (per `ComposeGeneral.dc.html`) embeds a Monaco editor with YAML syntax highlighting for editing and saving the YAML
- [ ] Covered by HTTP-seam tests per the spec's Testing Decisions
