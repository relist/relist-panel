# Spec — Volume lifecycle on deploy (orphans, fresh volumes, orphan cleanup)

## Problem

A deploy never touches a Volume's data, which is the right default, but it leaves the operator two
gaps:

1. **Orphan containers.** Removing a Service from a Compose's YAML and deploying leaves the old
   Service's container running. The panel treats the YAML as the source of truth, so the operator
   expects it gone.
2. **No way to wipe or reclaim data.** Data is destroyed only by deleting the Compose, Environment
   or Project (`down -v`). There is no "start this Compose over with empty volumes", and a named
   volume dropped from the YAML is orphaned on the host with nothing in the panel able to see or
   remove it. The Volume Explorer lists only Volumes in the current YAML.

Current behaviour, verified in code: `deploy` runs `docker compose up -d --build`
(`internal/dockerexec/dockerexec.go`); `stop` runs `down`; Compose delete and Project/Environment
teardown run `down -v` (`internal/composes/composes.go`, `internal/composes/teardown.go`).

## Goal and success criteria

- The plain Deploy action is always data-safe and leaves no orphan containers behind.
- Destroying data is a deliberate, per-action decision, never a persistent setting or a default.
- No path can delete a volume that belongs to a different Compose.
- Scope is limited to named volumes the Compose owns. Bind mounts (host paths) are never deleted.

## Out of scope

- A global, cross-Compose volume manager. Every operation is scoped to one Compose.
- Deleting a volume that is still declared in the Compose's YAML, other than through fresh volumes.
- Volume size and "in use" state in the orphan list (see Decisions).
- Anonymous volumes and explicitly named (`name:`) volumes in the orphan list.
- Orphans of a previous identity or a renamed project. They are labelled under the old project name
  and are not found by the current one.
- Changes to stop, restart, delete or teardown behaviour.

## Decisions

### Three independent pieces

| | Piece | Depends on |
| --- | --- | --- |
| A | `--remove-orphans` on plain deploy | nothing |
| B | Deploy with fresh volumes | shares a prepare step with deploy |
| C | List and delete orphan volumes | extends `volumes.CLI` |

### A — plain deploy removes orphan containers

`Runner.Up` becomes `docker compose up -d --build --remove-orphans`. The flag removes containers of
Services no longer in the YAML; it never removes volumes. The legacy-identity guard
(`requireUniqueIdentity`, called before `Up`) already refuses to deploy a Compose whose project name
collides with another, so the flag cannot delete another Compose's containers through a shared name.

### B — Deploy with fresh volumes

`POST /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/deploy-fresh`, a
separate route rather than a flag on `deploy`, so the destructive path cannot be reached by a
malformed body to the safe one and the existing route's behaviour does not change.

- The request body carries `confirmName`, which must equal the Compose's name. The server checks it;
  the UI check is a convenience only. Mismatch is a client error and nothing runs.
- Order matters. The handler loads the Compose, checks identity and `confirmName`, and only then
  runs `down -v` against the compose and env files **already on disk** — the same files the Volumes
  payload resolved, so the volumes deleted are the volumes the confirmation dialog listed. If no
  compose file exists (never deployed) the down step is skipped, as Compose delete already does.
  Only after that are the new compose and env files written, followed by `Up`. A refused request
  therefore leaves the files on disk byte-identical.
- Deploy's steps are split so both handlers share them: load-and-check, write-files, complete (record
  the deploy time and answer).
- `down -v` removes every named volume the deployed Compose declares and nothing else.
- **Failure window.** The steps are not atomic. If `down -v` fails, nothing was removed and the
  error is reported as such. If `down -v` succeeds and then writing the files or `up` fails, the
  volumes are already gone: the response is 502 and its message states that volumes were removed and
  the Compose is stopped, so the operator can fix the cause and press Deploy.
- No coordination change is needed: `coordinatedProject` in `internal/httpapi/coordination.go`
  already gates every non-GET request under `/api/projects/{id}/`.

### C — Orphan volumes

An **Orphan Volume** is a named volume that (1) carries the label
`com.docker.compose.project=<this Compose's project name>`, (2) is named
`<project>_<logical>` where `<logical>` is its `com.docker.compose.volume` label, and (3) is not one
of the volumes the current YAML resolves to.

Condition 3 is evaluated against the Compose resolved with **every profile enabled**
(`docker compose --profile '*' … config`), so a volume mounted only by a profile-gated Service
(`profiles: [tools]`) still counts as declared and is never offered for deletion just because that
Service is not currently active. Known residual: a top-level volume that no Service mounts at all is
pruned by `docker compose config` and so cannot be recognised as declared; Compose never creates such
a volume itself, and the real-daemon check confirms it.

Condition 2 excludes volumes declared with an explicit `name:`, the only kind two Composes can
share: a shared volume carries the label of whichever project created it and would otherwise be
offered for deletion while another Compose still uses it. It also excludes anonymous volumes, which
have no declared source. Both label names are verified against a real daemon before the work is
called done.

- `volumes.CLI` gains two methods: `ListProjectVolumes(ctx, project)`
  (`docker volume ls --filter label=com.docker.compose.project=<name>`) and
  `RemoveVolume(ctx, name)` (`docker volume rm`, no `-f`, Docker's error returned as is). No new
  port (ADR-0003); both are faked in `volumes/faketest`.
- Deciding which listed volumes are orphans is a pure function in package `volumes`: labelled names
  minus the resolved current volume names. It is tested without Docker.
- `GET  …/composes/{composeID}/volumes/orphans` returns the orphans (name, created-at).
- `DELETE …/composes/{composeID}/volumes/orphans/{name}` re-derives everything server side before
  removing: the volume carries this project's label, is not in the current YAML, and the Compose's
  identity is not a colliding legacy identity. The client's list is never trusted.
- A volume still used by a container makes Docker refuse the removal; that is returned as 409 with
  Docker's message. There is no force option.
- A Compose with a colliding legacy identity gets a refusal from both routes, as other operations
  already do through `requireUniqueIdentity`.
- Deletion is one volume per request. There is no delete-all.

#### Why label discovery is safe enough

Labels only produce candidates. Deletion authority is the server-side re-derivation above plus
Docker's refusal to remove an in-use volume. Volumes declared `external: true` carry no Compose
label and therefore never appear, which is correct: the panel did not create them.

### v1 omits size and in-use state

`docker volume ls` reports neither. Getting them needs a heavier call (`docker system df -v`) or a
per-volume container query. v1 shows name and creation time only and lets Docker report "in use" at
delete time. Size can be added later without changing the route shape.

## User interface

Read Ant Design's `llms.txt` and `docs/GUIDELINES.md`, and check props against the installed `antd`
version, before touching `web/` (AGENTS.md).

- **Unavailable states**: when the Compose was never deployed (the Volumes payload is a 502) or the
  Volumes tab is hidden (`available: false`, the bare binary), the dropdown item is disabled with a
  tooltip saying why — the dialog would have nothing to list.
- **Deploy button** (`web/src/pages/ComposePage.tsx`): the primary action stays a plain Deploy. A
  dropdown beside it has one item, **Deploy with fresh volumes**.
- **Fresh volumes dialog**: lists the named volumes that will be deleted, states that bind mounts are
  not deleted, links to the tar.gz download so a backup can be taken first, and has a field for the
  Compose's name. The confirm button is enabled only when the typed name matches.
- **Volumes tab**: a section for volumes no longer in the YAML. Each row has its own delete with a
  confirmation stating the data is permanently lost. The section is disabled with an explanation for
  a colliding legacy identity.
- Copy says Compose, not stack (existing convention).

## Testing

Go tests use the existing fakes for `ComposeRunner` and `volumes.CLI`:

- `Up` argv contains `--remove-orphans`.
- `deploy-fresh` runs down-with-volumes before up; wrong or missing `confirmName` runs nothing; a
  failed down does not run up; a failed up after a successful down reports that volumes were removed.
- Orphan derivation: label match without YAML match is an orphan; a name in the current YAML is not;
  a volume without the label is never listed.
- Orphan delete refuses a name that fails any server-side check and a colliding legacy identity, and
  maps Docker's in-use error to 409.
- Both new mutating routes are covered by the coordination tests the way `deploy` is.

Frontend tests follow the existing `*-view.test.ts` pattern for the pure view logic (dialog
confirm-enabled rule, orphan section state).

Behaviour that depends on a real Docker daemon — the label filter, `volume rm` refusal of an in-use
volume, `--remove-orphans` leaving volumes alone — is not covered by the fakes and is verified by one
manual run against Docker before the work is called done.

## Documentation

- `CONTEXT.md`: add **Orphan Volume**.
- `README.md` Volume files section: deploy now removes orphan containers; describe fresh volumes and
  orphan cleanup, and that bind mounts are never deleted.
- No ADR change: the existing ports are extended, not added to.
