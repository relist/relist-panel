# 01 — Compose Variables: migration, API, merge into `.env`

**What to build:** Read `../spec.md` first. Add migration `00014_scoped_variables.sql` (take the next
free number if it has moved) with **all** tables from the spec's Data model, so the feature has one
migration: `compose_variables`, `variable_sets`, `variable_set_entries`, `service_bindings`, their
foreign-key indexes, and `composes.variables_changed_at`. Bump `database_test.go`'s latest-version
assertion (13 → 14).

Then the Compose Variables API under
`/api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/variables`
(GET/POST, PUT/DELETE `/{variableID}`). It has the same wire shape, key validation and secret flag as
`internal/environmentvariables`; reuse that validation rather than copying it. Every write stamps
`composes.variables_changed_at` in the same transaction, like `domains_changed_at`
(`internal/domains/store.go`, `inTx`). The Compose JSON gains `variablesChangedAt`.

In `prepareDeployFiles` (`internal/composes/composes.go`), merge Environment Variables and Compose
Variables in Go before `renderEnvFile`: a Compose Variable replaces the Environment Variable with the
same key; Environment keys keep their order, and Compose-only keys follow in ID order. Put the merge
in a pure function that ticket 03 extends with sets and the effective-variables view.

**Blocked by: —**

**Status:** ready-for-agent

- [ ] Migration applies on a database at version 13 and on an empty one; `database_test.go` asserts 14
- [ ] CRUD round-trips through a real migrated SQLite; duplicate key in one Compose → 409; invalid key → 400; Compose not in the route's Environment → 404
- [ ] Deleting a Compose cascades its Compose Variables
- [ ] Each write moves `variablesChangedAt`; reads do not
- [ ] Merge: override replaces in place, no key appears twice in `.env`, order as specified
- [ ] A Compose with no Compose Variables writes a `.env` byte-identical to before this change
- [ ] Interpolation works: `${KEY}` in YAML resolves to the Compose Variable's value on deploy
- [ ] No handler logs a value (extend the existing log-capture test pattern)
- [ ] `go test ./...`, `go vet ./...`, `gofmt -l` clean

## Comments
