# 08 — Compose-owned Variable Sets: docs

**What to build:** Bring the docs in line with ticket 06/07 (read `../spec.md`, revision note and
Language).

- `CONTEXT.md`: **Variable Set** now "belonging to one Compose" (spec Language wording); check
  **Binding** and **Environment** entries still read correctly.
- `docs/adr/0015-scoped-variables.md`: sets are owned by the Compose; record why (operators manage
  sets where they bind them; cross-Compose sharing traded away); delete removes bindings after a UI
  confirmation instead of a 409; binding checks the same Compose. Remove statements that are now false.
- `docs/GUIDELINES.md`: the Environment Variables tab has no sets card; the Compose Variables tab
  section (§4.6) lists the Variable Sets card; `GUIDELINES.md` domain-model line stays accurate.
- Grep README, GUIDELINES, ADRs and CONTEXT for "Environment's sets", "usedBy", "409" near sets, and
  fix anything now false.

**Blocked by: 06**

**Status:** ready-for-agent

- [ ] Every statement about set ownership, deletion and binding checks matches the code after 06/07
- [ ] No doc still says sets belong to an Environment or can be shared across Composes

## Comments
