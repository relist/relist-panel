# 06 — Compose-owned Variable Sets: backend

**What to build:** Read `../spec.md` first — the "Revised 2026-10-10" note at the top, then Goal,
Decisions ("Deleting a set removes its bindings", "A set can only be bound within its own Compose",
"Redeploy marker"), Data model and API. Variable Sets move from the Environment to the Compose.

- **Migration:** edit `internal/migrations/00014_scoped_variables.sql` in place (do NOT add 00015):
  `variable_sets.environment_id` → `compose_id INTEGER NOT NULL REFERENCES composes(id) ON DELETE
  CASCADE`, `UNIQUE (compose_id, name)`, index on `compose_id`. The branch is unmerged and the
  version stays 14. Keep `TestScopedVariablesMigrationUpgradesVersion13` passing.
- **API (`internal/variablesets`):** move the sets and entries routes under
  `/api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/variable-sets`
  (list, create, rename, delete, entries create/update/delete). Route scoping: a Compose outside the
  route's Environment/Project → 404, a set of another Compose → 404. List returns each set with
  `entries` and `boundServices: string[]` (sorted) in place of `usedBy`.
- **Delete:** no more 409. Deleting a set removes it and its bindings; if it had bindings, stamp the
  Compose's `variables_changed_at` in the same transaction.
- **Bindings:** `PUT .../bindings/{service}` rejects a set whose `compose_id` is not the route's
  Compose with 400 (replaces the cross-Environment check).
- **Stamps:** entry writes stamp the owning Compose only when the set has at least one binding;
  create and rename never stamp.
- Deploy files, warnings, merge and effective-variables keep working unchanged apart from where sets
  are loaded from (by Compose). Remove code that only served Environment ownership.

**Blocked by: —**

**Status:** ready-for-agent

- [ ] Migration 00014 creates `variable_sets.compose_id` with cascade + unique + index; database tests pass at version 14
- [ ] Sets/entries CRUD under the Compose route round-trips on real SQLite; name/key validation and uniqueness per Compose (same name allowed in two Composes)
- [ ] Wrong-Compose / wrong-Environment routes → 404 and change nothing
- [ ] Deleting a bound set → 204, bindings gone, Compose stamped; deleting an unbound set does not stamp
- [ ] Binding another Compose's set → 400, nothing changes
- [ ] Entry write on a bound set stamps; on an unbound set does not; create and rename never stamp
- [ ] Deleting a Compose with sets and bindings, and deleting its Environment, both succeed and leave no rows
- [ ] Existing deploy/preview/warnings/effective-variables tests pass, adjusted only for ownership
- [ ] No handler logs a value
- [ ] `go test ./...`, `go vet ./...`, `gofmt -l .` clean

## Comments
