# Spec — Scoped variables (Compose Variables and Variable Sets bound to Services)

Date: 2026-10-10.

Revised 2026-10-10 (tickets 06–08): Variable Sets belong to a **Compose**, not an Environment. They
are created, edited and bound on the Compose's Variables tab only, and can no longer be shared
across Composes. Every section below is updated to the revised design; tickets 01–05 shipped the
Environment-owned version first.

## Problem

Every variable the panel knows is an **Environment Variable**: it belongs to one Environment and
reaches every Compose in it. At deploy, `prepareDeployFiles` (`internal/composes/composes.go`)
renders all of them into one `.env` per Compose, and that file is used twice:

1. `docker compose --env-file` interpolates `${VAR}` in the YAML (`internal/dockerexec/dockerexec.go`).
2. `resolveCompose` appends it to the `env_file:` of **every Service** (ADR-0002).

That leaves two gaps:

- A value that only one Compose needs, such as `LOG_LEVEL=debug` for one Compose in `staging`, has
  to be an Environment Variable, so every other Compose gets it too.
- Every Service in every Compose receives every variable. A Stripe key needed by `api` is also inside
  `redis`, `worker` and every other container in the Environment. There is no way to keep a secret
  out of containers that do not use it.

## Goal and success criteria

- A Compose page has a **Variables** tab. Variables added there apply to that Compose only, override
  an Environment Variable with the same key, and work in `${VAR}` interpolation just like
  Environment Variables.
- A Compose holds named **Variable Sets** (e.g. `db-creds`, `stripe`), each a list of
  key/value pairs with the same secret flag as Environment Variables. The operator creates, edits
  and deletes them on the Compose's Variables tab; nothing about sets appears on the Environment
  screens.
- On the same tab, the operator binds the Compose's Variable Sets to individual Services. On the
  next deploy only those Services' containers receive the set's variables.
- One set can be bound to many Services of its Compose.
- For each Service, the operator can see the **effective** variables: the final value for each key
  and the layer it came from.
- A Compose with no Compose Variables and no bindings deploys a byte-identical `.env` and an
  identical resolved YAML to today, apart from what the existing code already produces.

## Out of scope

- Variable Sets shared across Composes, Environments or Projects.
- `${VAR}` interpolation from Variable Sets (see Decisions, "Sets reach containers only").
- A redeploy marker for Environment Variables themselves. They have none today, and this spec does
  not add one.
- Applying variable changes without a redeploy.
- Importing or exporting `.env` files.
- Reordering bindings by drag and drop. Order is the order of the tags in the select (see Precedence).

## Language

New domain terms, added to `CONTEXT.md` with this work:

**Compose Variable**:
A key-value pair scoped to one Compose. It overrides an Environment Variable of the same key for
that Compose only, and is interpolated into the Compose's YAML like one. It has the same secret flag.
_Avoid_: Local variable, Override (the overriding is a behaviour, not the entity).

**Variable Set**:
A named list of key-value pairs belonging to one Compose, delivered to the containers of the
Services it is bound to and to no others. Never interpolated into YAML.
_Avoid_: Env, Env group, Env file (the file is how it is delivered, not what it is).

**Binding**:
The link between one Service in a Compose and one Variable Set. Ordered per Service.
_Avoid_: Attachment, Mount.

The **Environment Variable** entry stays as it is, except that "interpolated into all of that
Environment's Composes" gains "unless a Compose Variable of the same key overrides it".

## Decisions

### Compose Variables merge in Go into the existing `.env`

`prepareDeployFiles` loads the Environment Variables and the Compose Variables and merges them in Go
before `renderEnvFile`: a Compose Variable replaces the Environment Variable with the same key.
Environment keys keep their current order; Compose-only keys follow in ID order. The file never
holds a key twice, so nothing depends on dotenv "last one wins".

Because the result is still the one `.env`, both interpolation and container environment work
unchanged, and `resolveCompose` does not change for this part.

### Sets reach containers only, through per-set files

Each bound set is written to its own file at deploy, `<compose dir>/sets/<setID>.env`, with
`renderEnvFile` and `envFilePerm` (0600). `resolveCompose` appends the paths of a Service's bound
sets to that Service's `env_file:`, after the panel's `.env`, in binding order.

Sets are never passed to `--env-file`. Interpolation applies to the whole file, and two Services can
bind sets that hold the same key with different values, so there is no single value to interpolate.

The file name uses the set's database ID, not its name, so renaming a set never changes the resolved
YAML (same reasoning as ADR-0011).

Rejected: one file per Service. It would duplicate a shared set's values into several files and make
the file list depend on which Services exist.

### The files on disk always match the resolved YAML

The resolved YAML on disk is reused by stop, restart, logs, terminal and volume commands, not just by
deploy. A path in `env_file:` that does not exist breaks all of them. So:

- Deploy (and deploy-fresh) removes `<compose dir>/sets/` and rewrites it with exactly the sets the
  resolved YAML references, in the same `writeDeployFiles` step that writes the YAML and `.env`.
- Files for sets that are no longer bound disappear on that deploy. They can hold secret values, so
  they are never left behind.
- Teardown already removes the whole Compose directory (`teardown.go`), so it needs no change.

### Precedence

From lowest to highest, for one container:

1. The operator's own `env_file:` entries in the YAML (already lower than the panel's `.env` today)
2. Environment Variables
3. Compose Variables (2 and 3 are merged into one `.env`)
4. Bound Variable Sets, in binding order. A later set wins over an earlier one.
5. The YAML's own `environment:` on that Service (Compose's own rule: `environment:` beats `env_file:`)

When two sets bound to the same Service share a key, the deploy goes ahead (the later set wins) and
returns a warning naming the key, the Service and both sets.

### A binding to a missing Service warns, it does not block

Bindings name a Service as text, like Domains do. If the saved YAML no longer declares that Service,
the deploy goes ahead and returns a warning through the existing `warnings` channel:
`variable set "db-creds" is bound to service "old-api", which the compose does not declare`.
The binding is kept, so a renamed Service can be re-bound without re-entering any values.

This differs from Domains on purpose: a missing Domain target means traffic goes nowhere, while a
missing binding only means a container that no longer exists would have had extra variables.

### Deleting a set removes its bindings, after a confirmation

A set and every Service it is bound to are on the same screen, so deleting a bound set no longer
needs a 409 round trip. `DELETE` removes the set and its bindings (FK `ON DELETE CASCADE`) and, when
it had any bindings, stamps the Compose's `variables_changed_at` in the same transaction. The UI's
delete confirmation names the bound Services ("Unbinds it from api, worker. Takes effect on the next
deploy."), so nothing is removed silently.

### A set can only be bound within its own Compose

The bindings API rejects a set whose `compose_id` differs from the route's Compose with 400.

### Redeploy marker

`composes.variables_changed_at` is stamped, in the same transaction as the write, by:

- any create, update or delete of that Compose's Compose Variables
- any change to that Compose's bindings
- any change to a set's entries, when the set is bound to at least one Service (a rename does not:
  set files are named by ID, so nothing deployed changes; creating a set does not: it is unbound)
- deleting a set that had bindings

The Compose page shows the redeploy banner when `variables_changed_at >= lastDeployedAt`, the same
way `domainsPendingRedeploy` (`web/src/pages/domains-view.ts`) works for Domains.

### Interpolation hint

Preview and deploy return a warning when the YAML references `${NAME}` or `$NAME`, `NAME` is not an
Environment or Compose Variable, and `NAME` is a key in a set bound to any Service in this Compose:
`${STRIPE_KEY} is only in variable set "stripe", which is not used for ${...} interpolation`.
This is the mistake the split between interpolated and container-only variables invites, so it is
caught up front.

### Effective variables come from the same Go code as the deploy

The merge and precedence rules live in one pure function in `internal/composes`, used by both the
deploy and a read endpoint for a Service's effective variables. The frontend never re-derives
precedence. Layer 1 and layer 5 (the YAML's own `env_file:` and `environment:`) are not resolved;
the view lists `environment:` keys from the saved YAML and marks them "set in YAML, wins".

## Data model

Migrations `00014_scoped_variables.sql` and `00015_compose_owned_variable_sets.sql` end at this
schema:

```sql
CREATE TABLE compose_variables (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    compose_id INTEGER NOT NULL REFERENCES composes(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    secret INTEGER NOT NULL DEFAULT 0,
    UNIQUE (compose_id, key)
);

CREATE TABLE variable_sets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    compose_id INTEGER NOT NULL REFERENCES composes(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (compose_id, name)
);

CREATE TABLE variable_set_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    set_id INTEGER NOT NULL REFERENCES variable_sets(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    secret INTEGER NOT NULL DEFAULT 0,
    UNIQUE (set_id, key)
);

CREATE TABLE service_bindings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    compose_id INTEGER NOT NULL REFERENCES composes(id) ON DELETE CASCADE,
    service TEXT NOT NULL,
    set_id INTEGER NOT NULL REFERENCES variable_sets(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    UNIQUE (compose_id, service, set_id)
);

ALTER TABLE composes ADD COLUMN variables_changed_at INTEGER;
```

Plus an index on each foreign key column, as the earlier migrations do.

00014 keeps its original Environment-owned `variable_sets` (`environment_id`), because a live
install had already applied it. 00015 rebuilds the three set tables in the shape above: each
Environment set is copied into every Compose of its Environment, entries included, and each binding
points at its own Compose's copy at the same position. A set in an Environment with no Compose is
dropped. Down collapses same-named copies back into one Environment set (lossy).

Key validation is the same as Environment Variables (`validateInput` in
`internal/environmentvariables`). A set name is 1–63 characters of lowercase letters, digits and
`-`, starting with a letter.

## API

All under `/api/projects/{projectID}/environments/{environmentID}`:

| Method and path | Purpose |
|---|---|
| `GET/POST /composes/{composeID}/variables` | List, create Compose Variables |
| `PUT/DELETE /composes/{composeID}/variables/{variableID}` | Update, delete one |
| `GET/POST /composes/{composeID}/variable-sets` | List the Compose's sets (with entries and `boundServices: ["api", ...]`), create one |
| `PUT/DELETE /composes/{composeID}/variable-sets/{setID}` | Rename; delete (also removes its bindings) |
| `POST /composes/{composeID}/variable-sets/{setID}/entries` | Add an entry |
| `PUT/DELETE /composes/{composeID}/variable-sets/{setID}/entries/{entryID}` | Update, delete an entry |
| `GET /composes/{composeID}/bindings` | Every binding of the Compose, grouped by Service |
| `PUT /composes/{composeID}/bindings/{service}` | Replace one Service's bindings: `{"setIds": [3, 7]}`, order = position. `[]` removes them all |
| `GET /composes/{composeID}/services/{service}/effective-variables` | `[{key, value, secret, source, overrides}]`, where `source` is `environment`, `compose` or `{set: {id, name}}` and `overrides` lists the shadowed layers |

The Compose JSON gains `variablesChangedAt`, like `domainsChangedAt`.

The `{service}` path segment is not required to exist in the saved YAML, so stale bindings can be
listed and removed.

## UI

Follow `docs/GUIDELINES.md`, and read Ant Design's `llms.txt` before changing `web/` (AGENTS.md).

**Environment's Variables tab** (`ProjectPage.tsx`): only the existing Environment Variables card,
with the note "Used by every Compose · usable as `${VAR}`". No Variable Sets card.

**Compose page**, new **Variables** tab between Editor and Domains (`ComposePage.tsx`):

- **Compose variables** card: the `VariablesPanel` editor. A row whose key also exists as an
  Environment Variable shows "overrides <environment>: <value or ••••>".
- **Variable Sets** card: the Compose's sets as collapsible rows with name, entry count and
  "bound to" Service tags. Expanding a row shows the same editor as `VariablesPanel`, reused rather
  than copied. "New set" opens a small name form; rename in place; delete asks for confirmation and
  names the Services it unbinds. Note: "Only for Services you bind · not usable as `${VAR}`".
- **Service bindings** card: one row per Service in the saved YAML, using the same Service list the
  Domains form gets (`DomainFormModal`). Each row has a `Select mode="multiple"` of this Compose's
  sets. Tag order is binding order. Bindings for Services not in the YAML appear below with a warning
  icon and "not in YAML" and can only be removed.
- Each Service row has an **Effective env** button that opens a modal backed by the
  effective-variables endpoint: key, masked or revealed value, source tag, and the YAML
  `environment:` keys marked "set in YAML, wins".
- The redeploy banner follows `variablesChangedAt` in addition to `domainsChangedAt`.

The Editor tab's existing "Environment" card ("N variables applied at deploy time") also counts
Compose Variables.

When a Compose has no Compose Variables and no sets, its cards show only their empty state and a
single add button (the bindings card says to create a set first). Nothing else changes for an operator who does not use them.

## Docs

- `CONTEXT.md`: add Compose Variable, Variable Set and Binding (above), and amend Environment
  Variable.
- New `docs/adr/0015-scoped-variables.md`: records that the panel no longer gives every Service the
  same variables, why sets are container-only, the per-set file, and the precedence. It amends
  ADR-0002's "one env file injected into every Service".

## Tickets

Five, each ending in a working, tested state:

- `01-compose-variables-backend.md` — migration (all tables, so there is one migration), Compose
  Variables API, the Go merge into `.env`, `variables_changed_at`. (No blockers.)
- `02-compose-variables-ui.md` — Compose page Variables tab with the Compose variables card,
  override hint, redeploy banner. (Blocked by 01.)
- `03-variable-sets-backend.md` — sets and entries API, bindings API, per-set files in
  `writeDeployFiles`, `resolveCompose` binding injection, the missing-Service, duplicate-key and
  interpolation warnings, 409 on delete, effective-variables endpoint. (Blocked by 01.)
- `04-variable-sets-ui.md` — Variable Sets card on Environment Settings, Service bindings card,
  Effective env modal. (Blocked by 02 and 03.)
- `05-docs.md` — CONTEXT.md and ADR-0015. (Blocked by 03.)

Revision (sets owned by the Compose):

- `06-compose-owned-sets-backend.md` — migration 00015 (00014 restored), sets API moved under the Compose,
  delete removes bindings, same-Compose binding check, stamps. (No blockers.)
- `07-compose-owned-sets-ui.md` — Variable Sets card moved to the Compose Variables tab, removed from
  the Environment. (Blocked by 06.)
- `08-compose-owned-sets-docs.md` — CONTEXT.md, ADR-0015, GUIDELINES. (Blocked by 06.)

## Verification

- Backend: `go test ./...`, `go vet ./...`, gofmt. Tests must cover:
  - the merge (override, order, no duplicate keys)
  - per-set files written and stale ones removed on redeploy
  - `resolveCompose` output for bound, unbound and missing Services
  - each warning
  - deleting a bound set removes its bindings and stamps the Compose
  - a binding to another Compose's set refused
  - deleting a Compose, and an Environment, with bound sets succeeding
  - a Compose with no new data producing the same `.env` and YAML as before
- A real deploy on the dev host: a Service bound to a set has the variable inside its container
  (`docker compose exec <svc> env`) and an unbound Service does not. Stop and terminal still work
  after unbinding and redeploying.
- Frontend: `pnpm --dir web typecheck`, `test`, `build`. Check the new cards' empty, filled,
  stale-binding and secret-masked states, and the Effective env modal, at desktop and phone width.
