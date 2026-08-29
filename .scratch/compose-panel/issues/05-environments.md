# 05 — Environments

**What to build:** Within a Project, an operator can create and delete Environments, and Project creation automatically provisions a default "production" Environment.

**Blocked by:** 04

**Status:** ready-for-agent

- [ ] A migration creates the `environments` table (id, project_id FK, name, created_at), unique on (project_id, name), per `docs/sa.md`
- [ ] Creating a Project also creates a default Environment named "production" (spec user story 12) — this extends the Project-creation handler from ticket 04
- [ ] The Project detail page shows Environments as Tabs (per `ProjectDetail.dc.html`), defaulting to "production"
- [ ] An operator can create an additional Environment with a name
- [ ] An operator can delete an Environment
- [ ] Covered by HTTP-seam tests per the spec's Testing Decisions
