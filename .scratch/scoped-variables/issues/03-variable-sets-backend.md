# 03 — Variable Sets and Bindings: API, deploy files, warnings, effective variables

**What to build:** Read `../spec.md` (Decisions, Data model, API). The tables exist from ticket 01.

- **Sets API** under `/environments/{environmentID}/variable-sets`: list (each set with its entries
  and `usedBy: [{composeId, composeName, service}]`), create, rename, delete, plus entries
  CRUD under `/{setID}/entries`. A set name is 1–63 chars of `[a-z0-9-]` starting with a letter, unique per
  Environment. Entries use the Environment Variable key validation and secret flag. Deleting a set
  that is still bound → 409 with the binders in the body.
- **Bindings API**: `GET /composes/{composeID}/bindings` grouped by Service;
  `PUT /composes/{composeID}/bindings/{service}` with `{"setIds": [...]}` replaces that Service's
  bindings, array order = `position`, `[]` removes them all. A set from another Environment → 400.
  `{service}` need not exist in the saved YAML.
- **Redeploy marker**: binding changes stamp the Compose's `variables_changed_at`; set renames and
  entry writes stamp every Compose that binds the set, in the same transaction.
- **Deploy**: `writeDeployFiles` removes `<compose dir>/sets/` and rewrites it with one
  `<setID>.env` per set referenced by the resolved YAML (`renderEnvFile`, `envFilePerm`).
  `resolveCompose` appends each Service's bound set paths to its `env_file:` after the panel's `.env`,
  in position order. Sets are never passed to `--env-file`. Preview resolves the same paths but reads
  no values.
- **Warnings** through the existing channel, from preview and deploy: a binding to a Service the YAML
  does not declare; two sets bound to one Service sharing a key (name the key, Service and both
  sets); `${NAME}`/`$NAME` in the YAML that is not an Environment or Compose Variable but is a key in
  a set bound in this Compose.
- **Effective variables**: `GET /composes/{composeID}/services/{service}/effective-variables`
  returns `[{key, value, secret, source, overrides}]` from the same pure merge function the deploy
  uses (extend ticket 01's). It also lists the saved YAML's `environment:` keys for that Service as
  `source: "yaml"` with no value resolution.

**Blocked by: 01**

**Status:** closed

- [ ] Sets and entries CRUD round-trip on real SQLite, including name and key validation and uniqueness
- [ ] Delete of a bound set → 409 listing binders; after unbinding it succeeds
- [ ] Deleting an Environment that has bound sets succeeds (cascade order)
- [ ] Cross-Environment binding → 400
- [ ] `resolveCompose` tests: bound Service gets the set paths in order after `.env`; unbound Service gets only `.env`; a Service with its own `env_file` string or list keeps it first
- [ ] Redeploy after unbinding removes the stale `sets/<id>.env`; files are 0600
- [ ] Each of the three warnings fires, and none fire for a Compose with no bindings
- [ ] A Compose with no bindings produces resolved YAML identical to before this change
- [ ] Effective variables match what the deploy writes for the same data (one test drives both)
- [ ] No handler logs a value
- [ ] Manual check on the dev host: `docker compose exec <bound svc> env` shows the variable, an unbound Service does not; stop and terminal still work after unbind + redeploy
- [ ] `go test ./...`, `go vet ./...`, `gofmt -l` clean

## Comments
