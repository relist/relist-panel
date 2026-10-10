# Scoped variables: Compose Variables and Variable Sets bound to Services

Until now every variable the panel knew was an Environment Variable, and every one reached every Service. The one `.env` per Compose was used twice: `docker compose --env-file` interpolated `${VAR}` in the YAML, and the same file was appended to the `env_file:` of every Service (ADR-0002). A value only one Compose needed had to be an Environment Variable, so every other Compose got it too. A secret needed by one Service still reached every container in the Environment.

**Decision:** this ADR amends ADR-0002's "one env file injected into every Service". Services no longer all receive the same variables. The Environment's `.env` still goes into every Service's `env_file:` (ADR-0014's "the `env_file` injection still covers every Service" stays true for it), but two things are added on top:

- **Compose Variables** are merged into the Compose's `.env` in Go, by `mergeVariables` (`internal/composes/envfile.go`), before `renderEnvFile`. A Compose Variable replaces the Environment Variable with the same key. Interpolation and container environment work as before, and the file never holds a key twice, so nothing depends on dotenv's last-one-wins.
- **Variable Sets** are bound to individual Services. Each bound set is written to its own file, `<compose dir>/sets/<setID>.env`, with mode 0600. `resolveCompose` (`internal/composes/composes.go`) appends that Service's set paths to its `env_file:` after the panel's `.env`, in binding order. The file is named by the set's database ID, not its name, so renaming a set never changes the resolved YAML (ADR-0011).

Sets are never passed to `--env-file`, so they are never interpolated into `${VAR}`. Interpolation applies to the whole file, and two Services can bind sets that hold the same key with different values, so there is no single value to interpolate. Rejected: one file per Service. It would duplicate a shared set's values into several files, and the file list would depend on which Services exist.

From lowest to highest, the precedence for one container is:

1. The operator's own `env_file:` entries in the YAML
2. Environment Variables
3. Compose Variables (2 and 3 are one `.env`)
4. Bound Variable Sets, in binding order. A later set wins over an earlier one.
5. The Service's own `environment:` in the YAML, which Compose already ranks above `env_file:`

The panel does not resolve layers 1 and 5. The effective-variables view (`GET .../services/{service}/effective-variables`) lists the YAML's `environment:` keys and marks them "set in YAML, wins".

**The files on disk always match the resolved YAML.** The resolved YAML is reused by stop, restart, logs, terminal and volume commands, and a missing `env_file:` path breaks all of them. So a deploy removes `<compose dir>/sets/` and rewrites it in `writeFiles` (`internal/composes/composes.go`), alongside the YAML and `.env`, with exactly the sets the resolved YAML references. Sets no longer bound disappear on that deploy, because they can hold secret values. Teardown already removes the whole Compose directory, so it needs no change.

**A binding to a Service the YAML does not declare warns; it does not block.** Bindings name a Service as text, as Domains do. The deploy goes ahead, the warning goes through the deploy's `warnings`, and the binding is kept, so a renamed Service can be re-bound without re-entering values. This differs from Domains on purpose: a missing Domain target sends traffic nowhere, while a missing binding only means a container that no longer exists would have had extra variables. Two sets bound to one Service that share a key also warn, naming the key, the Service and both sets; the later set wins.

**Deleting a bound set returns 409, not a cascade.** `DELETE` answers 409 with `usedBy`, the Compose and Service names that bind the set, and the operator unbinds first. This stops the next deploy from silently removing secrets from a running Service. The `service_bindings.set_id` foreign key still uses `ON DELETE CASCADE`, so deleting a whole Environment never fails on cascade ordering; the 409 is enforced in the API, not the schema. A binding may only name a set in the Compose's own Environment, and anything else is refused with 400.

A Compose records `variables_changed_at` when its Compose Variables or bindings change, or when a set it binds is edited, so the Compose page shows the same redeploy banner as Domains.

## Considered options

**One file per Service.** Rejected, as above: it duplicates shared values and ties the file list to the Services that exist.

**Passing sets through `--env-file`.** Rejected: interpolation sees one value per key for the whole file, and sets differ per Service.

## Consequences

- **A Compose with no Compose Variables and no bindings deploys the same `.env` and resolved YAML as before.**
- **A set's values cannot be used for `${VAR}`.** A YAML reference to a key that only a set holds gets a deploy warning naming the set. To interpolate a value, the operator makes it a Compose Variable or an Environment Variable.
- **Unbinding is a separate step before deleting a set.** That is deliberate: a set in use cannot vanish under a running Service.
- **Variable changes apply on the next deploy.** The Compose page shows the redeploy banner until one happens.
- **Secret values in sets are written to disk** in each Compose's `sets/` directory at 0600, rewritten on every deploy, as the `.env` already is.
