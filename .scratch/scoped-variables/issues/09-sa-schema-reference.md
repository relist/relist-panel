# 09 — Bring `docs/sa.md` up to date with migrations 00014 and 00015

**What to build:** `docs/sa.md` (schema reference) has no entries for `compose_variables`,
`variable_sets` (owned by `compose_id`), `variable_set_entries`, `service_bindings` or
`composes.variables_changed_at`. Add them in the file's existing style, from
`internal/migrations/00014_scoped_variables.sql` and
`internal/migrations/00015_compose_owned_variable_sets.sql` (the final shape). Pre-existing staleness found in the final review of
scoped variables; not a merge blocker.

**Blocked by: —**

**Status:** needs-triage

- [ ] Every table and column after 00015 is described in `docs/sa.md`, matching the migration

## Comments
