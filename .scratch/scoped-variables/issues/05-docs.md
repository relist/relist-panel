# 05 — Docs: CONTEXT.md and ADR-0015

**What to build:** Read `../spec.md` (Language, Decisions, Docs) and use the domain-modeling skill's
conventions.

- `CONTEXT.md`: add **Compose Variable**, **Variable Set** and **Binding** as written in the spec's
  Language section, and amend **Environment Variable** ("unless a Compose Variable of the same key
  overrides it"). Keep the existing entry style (definition, then `_Avoid_:`).
- `docs/adr/0015-scoped-variables.md` (next free number if taken), matching the existing ADRs'
  format. Record that Services no longer all receive the same variables; why sets are
  container-only and never interpolated; the per-set file named by set ID; that the files on disk
  always match the resolved YAML; the precedence order; warn instead of block for missing Services;
  409 instead of cascade for deleting a bound set. State that it amends ADR-0002's "one env file
  injected into every Service", and add a pointer to it in ADR-0002.

**Blocked by: 03**

**Status:** closed

- [ ] Terms match the code's identifiers as shipped in 01–03 (fix the docs, not the code, if they diverged)
- [ ] ADR-0002 links to ADR-0015
- [ ] No other doc still claims every Service gets every variable (`grep` README, GUIDELINES, ADRs)

## Comments
