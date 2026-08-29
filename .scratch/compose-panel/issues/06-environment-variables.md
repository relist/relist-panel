# 06 — Environment Variables

**What to build:** Within an Environment, an operator can manage a set of key-value Environment Variables, scoped to that Environment and shared by every Compose inside it.

**Blocked by:** 05

**Status:** ready-for-agent

- [ ] A migration creates the `environment_variables` table (id, environment_id FK, key, value), unique on (environment_id, key), per `docs/sa.md`
- [ ] The Environment Variables sub-tab (per `EnvironmentVariables.dc.html`) lists all variables for the active Environment
- [ ] An operator can add, edit, and delete a variable
- [ ] Covered by HTTP-seam tests per the spec's Testing Decisions
