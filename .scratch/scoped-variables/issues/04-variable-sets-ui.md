# 04 — Variable Sets and Bindings: UI

**What to build:** Read `../spec.md` (UI section), Ant Design's `llms.txt` and `docs/GUIDELINES.md`.

- **Environment Settings** (`web/src/pages/ProjectPage.tsx`, where `VariablesPanel` is mounted): add
  the note "Used by every Compose · usable as `${VAR}`" to the existing Variables card. Add a
  **Variable Sets** card: collapsible rows with name, entry count and "used by" tags
  (`compose/service`); expanding a row shows the generalised `VariablesPanel` from ticket 02. Note:
  "Only for Services you bind · not usable as `${VAR}`". "New set" opens a name form; rename in
  place. A 409 on delete shows who still binds the set.
- **Compose page Variables tab**: add a **Service bindings** card below Compose variables. One row
  per Service in the saved YAML (same source as the `DomainFormModal` Service list), each with a
  `Select mode="multiple"` of the Environment's sets; tag order is binding order and saving calls the
  PUT. Bindings for Services not in the YAML appear below with a warning icon and "not in YAML", and
  can only be removed.
- **Effective env** button per Service row opens a modal backed by the effective-variables endpoint:
  key, masked or revealed value, a source tag (Environment / Compose / set name / "set in YAML,
  wins"), and the shadowed layers.

Rebuild the embedded frontend and commit it separately.

**Blocked by: 02, 03**

**Status:** closed

- [ ] Create, rename, delete a set; edit its entries with secret masking; "used by" reflects bindings
- [ ] Bind, reorder and unbind sets per Service; the redeploy banner follows
- [ ] Stale-binding row appears when a bound Service is removed from the YAML and can be removed
- [ ] Effective env modal shows the right source and overrides for a Service with Environment, Compose, two sets and a YAML `environment:` key
- [ ] Deploy warnings from ticket 03 appear where deploy and preview warnings already show
- [ ] Empty states: an Environment with no sets and a Compose with no bindings look and behave as before apart from the empty cards
- [ ] Desktop and phone width checked
- [ ] `pnpm --dir web typecheck`, `test`, `build` clean; view-logic unit tests for row grouping and stale detection

## Comments
