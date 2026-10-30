# Spec — Zero-downtime deploy for Services with a Domain

Date: 2026-10-30.
Status: open — spec under review, no branch yet

## Problem

A Deployment ends with `docker compose up -d --remove-orphans`. For every Service whose
configuration or image changed, Compose stops the old Container and then creates the new
one. Until the new one has started and its app is ready, Traefik has no backend for the
Service's Domains and answers 404 or 502. On the spike's test app, which takes 6 s to
boot, one deploy produced about 16 s of errors: 320 × 404 and 4 × 502 at ~20 req/s.

Compose has no rolling update. Rolling updates (`deploy.update_config`) exist only in
Swarm, which this panel does not use.

## Goal and success criteria

- On the Compose's Domains tab, the operator turns on **Zero-downtime deploy** per
  Service. Only Services with at least one Domain show the switch.
- When a deploy changes only the app (image, code, env) and leaves the Service's
  `traefik.*` labels as they were, a Service with the switch on serves every request
  through the deploy. The spike measured 0 errors in 180 requests sent while a replica
  was being stopped, with no request received twice.
- The new Container only takes traffic after its Docker `healthcheck` reports
  `healthy`. If it doesn't get there, the panel removes it, the old Container keeps
  serving, and the Deployment fails with a log entry that names the Service.
- If the switch is on and the Service can't be rolled (no `healthcheck`, a
  `container_name`, a fixed host port, or `deploy.replicas` > 1), the panel refuses the
  deploy before queueing it, and the web shows a modal that lists Service → reason.
- Services with the switch off (databases, workers, anything else) deploy exactly as
  today.
- Nothing changes in the install's `docker-compose.yml`.

## What is not promised

The UI help text under the switch says these, and so does the README:

- **The healthcheck has to mean "ready".** If it only checks that the process is alive,
  Traefik sends traffic before the app can answer.
- **The app has to shut down gracefully on SIGTERM.** If the old Container drops a
  request it is still working on when it's stopped, that request gets a 502. Traefik
  does not retry a request that has already reached the app (proven for every method,
  below).
- **Other Services still restart.** If a deploy recreates `db`, the app may return
  errors of its own while `db` is down. Compose only recreates `db` when its image or
  configuration changed.
- **A deploy that changes the Service's Traefik labels has a short outage**, logged as
  a warning. The spike measured 1.9–2.0 s of 404 over 3 runs, with the old Container
  stopped as soon as the new one was healthy. This covers an edited Domain, the first deploy after the
  switch is turned on, and edited hand-written `traefik.*` labels. See "Label drift".

## Out of scope

- Zero-downtime deploy for Services without a panel Domain, including Services that
  route only through hand-written Traefik labels.
- `deploy.replicas` > 1. It is refused, not rolled. The spike did not test it.
- Keeping router names stable while labels change (versioned router names per
  Deployment). Recorded as the rejected option in the ADR.
- Rolling back to the old version once the new Container has passed its healthcheck.
- Rolling a Service that has anonymous volumes. It is refused (see "Checks when
  queueing" and step 3.2 of the Deploy sequence). On recreate, Compose hands the old
  Container's anonymous volumes to the new one. A Container created by `--scale` gets
  new, empty ones instead, so after a rollout the app would see an empty directory.
- Changing Traefik's static configuration.

## Decisions and evidence

All spikes ran on Docker Compose v5.5.1 and Traefik v3.7, without Swarm, in throwaway
stacks under the session scratchpad.

1. **Rollout = scale to 2 without recreating, wait for healthy, stop and remove the
   old Container.** `up -d --no-deps --no-recreate --scale web=2 web` creates one new
   Container from the current file and leaves the old one untouched. The spike checked
   the env of both Containers. `--scale` isn't stored anywhere: after the old Container
   is removed, a later plain `up` sees one Container, matching the file, and doesn't
   recreate it (same ID). 20+ rollouts in a row left one Container each time, numbered
   `app-1` … `app-22`.
2. **The old Container is removed (`rm`), not only stopped.** A stopped Container keeps
   its project labels, so the next `up` would see two and pick one to scale down. The
   panel removes the old one by ID. It never uses `--scale web=1`, which would let
   Compose choose which one to remove.
3. **Traefik doesn't route to a Container that is still `starting`.** No request reached
   the new Container before its health turned `healthy`.
4. **Stopping the old Container drops a request unless Traefik retries it.** Traefik
   drops a dead server only after it rebuilds its config. Until then, a request sent to
   the dead server failed in 3 of 4 rollouts.
5. **Fix: a `retry` middleware (`attempts=4`) plus a 1 s dial timeout, per Service.**
   With Traefik's default `dialTimeout` of 30 s, a dial to the dead Container hung for
   14–44 s. With the default 30 s and `attempts=2`, one request in 30 still failed. With
   `dialTimeout: 1s` and `attempts=4`: 180/180 OK across POST, GET and PUT, after both
   `docker stop` and `docker kill`. Slowest request ~3 s. Every request reached the app
   exactly once (counted by request id).
6. **The retry never duplicates a request.** The spike killed a Container while it held
   a request (POST, PATCH, PUT, GET, DELETE, twice each). The client got 502 and the app
   had received the request once. Traefik retried only when the dial failed, which
   means the request was never sent.
7. **The dial timeout is a `serversTransport` in the file provider, referenced per
   Service.** This was chosen over the static `--serversTransport.forwardingTimeouts.dialTimeout`,
   which would change every route and need an edit to the install file. A label
   `loadbalancer.serversTransport=relist-fastdial@file` applied it to that Service only.
   A Service on the same Traefik without the label kept the default. Traefik restarted
   cleanly with the file present. **If the file is missing, the Service's Traefik
   service is disabled and its Domains return 404** (`servers transport not found
   relist-fastdial@file`). They recover within seconds once the file is back.
8. **Different labels on the old and new Container conflict.** When the two Containers
   define the same router or middleware with different values, Traefik logs `defined
   multiple times with different configurations` and drops the router. The Domain
   returned 404 for the whole overlap (79 of ~230 requests). With identical labels:
   210/210 OK. When the old Container is stopped as soon as the new one is healthy, the
   404s last 1.9–2.0 s (3 runs, 34–35 requests each).
9. **`docker pause` on the old Container doesn't drain it.** Traefik kept routing to it,
   which made things worse.

## Domain changes

- `CONTEXT.md` gains:

  **Zero-downtime deploy**:
  A per-Service setting, available to Services with a Domain. When it is on, a
  Deployment replaces the Service's Container by **Rollout** instead of recreating it.
  _Avoid_: Rolling update (Swarm's feature), Blue/green.

  **Rollout**:
  The step of a Deployment that starts a new Container of one Service next to the old
  one, waits until it is healthy, then stops and removes the old one. If the new one is
  not healthy in time, it is removed instead and the Deployment fails.
  _Avoid_: Update, Swap.

- **ADR-0023** records the decisions above: rollout through Compose's `--scale`, retry
  and transport injected per Domain, the transport in the file provider, and label
  drift accepted as a short outage. The rejected options are versioned router names, a
  static dial timeout, Swarm, and blue/green under a second project name (a second
  project name gets its own, empty named volumes, and ADR-0005 freezes the project
  name). It extends ADR-0013's label injection.

## Data model

Migration `00024_zero_downtime_services.sql`, a new file. Committed migrations are never
edited.

```sql
-- Services of a Compose whose deploys roll out instead of recreating (ADR-0023).
CREATE TABLE zero_downtime_services (
    compose_id INTEGER NOT NULL REFERENCES composes(id) ON DELETE CASCADE,
    service TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (compose_id, service)
);
```

- A row means the switch is on. Turning it off deletes the row.
- A row whose Service is no longer in the YAML, or no longer has a Domain, is ignored
  everywhere. It's left in place, so renaming a Service back restores the setting.
- Server Migration (ADR-0018) must carry the table. The plan checks whether Migration
  copies `panel.db` whole or table by table, and adds the table to the list if needed.

## The transport file

- The file is `<data dir>/.traefik/dynamic/relist-transports.yml`:
  ```yaml
  http:
    serversTransports:
      relist-fastdial:
        forwardingTimeouts:
          dialTimeout: 1s
  ```
- The panel writes it at startup, always, whether or not any switch is on. It also
  rewrites it at the start of every Deploy step that injects the label. It writes
  through a temporary file and a rename, as `certificates.writeFile` does. The panel
  never deletes it.
- The Certificates reconcile removes only files matching `cert-*.yml`, so it leaves
  this file alone. A test pins that.
- If the write fails at startup, the panel logs it and keeps running. If it fails at the
  start of a Deploy step, that Deployment fails with "could not write Traefik transport
  file" before any Container is touched.
- The file provider has to be on, which it is from ADR-0022. The README's "Upgrading an
  existing install" section states that, because the panel cannot read Traefik's static
  config.

## Labels

For a Service whose switch is on, `domains.Inject` adds two things to each of its
Domains, named `n = relist-c<composeID>-d<domainID>`:

```text
traefik.http.middlewares.<n>-retry.retry.attempts=4
traefik.http.services.<n>.loadbalancer.serversTransport=relist-fastdial@file
```

It also puts `<n>-retry` **last** in the router's `middlewares` chain, after strip,
prefix and the user's own middlewares, right in front of the service. Only the dial to
the backend needs retrying. In last place, a retried attempt can't run stripprefix or
addprefix twice on a request that was already changed, and it can't call the user's
forwardAuth or rateLimit twice. The HTTP→HTTPS redirect router doesn't get it. Services with the switch off get no new
labels, so their resolved file stays byte-for-byte what it is today.

The set of Services with the switch on is read when the Deployment is queued and stored
in the execution snapshot (ADR-0017), next to the YAML it shapes. Changing a switch
afterwards does not affect a Deployment that is already queued.

## Checks when queueing

These run in `POST …/deployments` after `CheckDeployable`, against the same resolved
files that go into the snapshot. That covers the Compose page, Redeploy and a Project
deploy alike.

The stored YAML keeps `${VAR}` references, and Compose fills them in from the `.env`
file the panel writes. So the checks never parse the raw YAML. They read the output of
`docker compose config --format json` run over the snapshot's files: YAML, env file and
Variable Set files, staged in a temporary directory, as the build plan already stages
them. The panel already runs `compose config` for volume plans. `ports:
["${PORT}:80"]` and `deploy.replicas: ${N}` are therefore checked with their real
values.

For each Service with the switch on and at least one Domain:

| Problem | Reason shown |
|---|---|
| no `healthcheck`, `healthcheck.disable: true`, or `test: ["NONE"]` | "has no healthcheck" |
| `container_name` set | "sets container_name; two Containers can't share a name" |
| a `ports:` entry that publishes a fixed host port (`"8080:80"`, long syntax with `published`) | "publishes host port 8080; two Containers can't bind it" |
| `deploy.replicas` or `scale` greater than 1 | "runs more than one replica; rollout supports one" |
| a `volumes:` entry of type `volume` with no `source` (e.g. `- /data`) | "has an anonymous volume at /data; mount a named volume there" |

If any Service fails, the endpoint answers **422** and creates no Deployment:

```json
{ "error": "zero-downtime deploy is on for services that can't be rolled out",
  "zeroDowntime": [ { "service": "web", "reasons": ["has no healthcheck"] } ] }
```

A port with no host side (`"80"`) is fine, because Docker picks a free host port for
each Container.

These cases are not refused. The Deployment runs the ordinary `up` for that Service and
records a warning:

- **No running Container of the Service** (first deploy, or the Compose is stopped):
  "web had no running container; deployed without rollout".
- **Fresh volumes**: the Deployment already runs `down -v`. The Fresh volumes confirm
  modal adds a line: "Zero-downtime deploy doesn't apply to this deploy."

## Deploy sequence

`dc` is `docker compose` with the project name and resolved file the runner already
passes. The example: `web` has the switch on, `worker` and `db` don't.

1. **Fetch** and **Build** are unchanged.
2. **Deploy step**, inside the Project gate and shielded from Cancel, as today:
   1. Write the resolved files, as today (with the labels above), then the transport
      file.
   2. If Fresh volumes is on, run `down -v` and then the ordinary
      `dc up -d <build flag> --remove-orphans` for the whole Compose, as today. There
      are no rollout steps, and the Deployment ends here.
   3. Run `dc up -d <build flag> --no-deps --remove-orphans worker db`, which lists
      every Service that won't roll out in this Deployment. The build flag is
      `--no-build`, or `--build` for a Deploy-mode Compose without a Source, as today.
      If no Service is left to list, skip this command.
3. **One `rollout:<service>` step per Service with the switch on**, in YAML order, in
   the same gate hold and with the same shielding:
   1. Through the Engine API, list Containers with labels
      `com.docker.compose.project=<project>`, `com.docker.compose.service=web` and
      `com.docker.compose.oneoff=False`, in state `running`. Call this set OLD.
      - If OLD is empty, run `dc up -d <build flag> --no-deps web`, record the "no
        running container" warning, and end the step.
   2. Through the Engine API, inspect the mounts of every OLD Container. If any of them
      is an anonymous volume, the step fails here, before NEW is created. That catches
      a Dockerfile `VOLUME` nobody mounted over, which is invisible at queue time
      because the image may not be built yet. An anonymous volume is a mount of type
      `volume` whose volume has no `com.docker.compose.volume` label. The error reads:
      "web has an anonymous volume at /var/lib/app (from the image's VOLUME); mount a
      named volume there or turn off zero-downtime deploy". Nothing about `web` has
      changed. Other Services were already updated in step 2.3, as with any rollout
      failure.
   3. Through the Engine API, read the `traefik.*` labels of one OLD Container. Compare
      them with the Service's `traefik.*` labels from `dc config --format json` on the
      files just written. Both sides are interpolated values, so a hand-written label
      such as ``Host(`${HOST}`)`` doesn't count as drift when the value is the same. If
      they differ, the step is in **drift mode** (see below).
   4. Run `dc up -d <build flag> --no-deps --no-recreate --scale web=<len(OLD)+1> web`.
   5. List again. NEW is the one Container that isn't in OLD. If there isn't exactly
      one, the step fails ("could not identify the new container").
   6. Inspect NEW once a second until `State.Health.Status` is `healthy`, it is
      `unhealthy`, `State.Status` is no longer `running`, or the wait limit passes.
   7. If it's **healthy**: in drift mode, stop OLD straight away. Otherwise, wait 3 s so
      Traefik's config (rebuilt every 2 s) includes NEW, then stop OLD. Stop each OLD
      Container through the Engine API with no `t`, so Docker uses its `StopTimeout`,
      which Compose sets from `stop_grace_period`. Then remove it with `v=false`.
   8. If it's **not healthy**: write the last health log entry
      (`State.Health.Log[-1].Output`, cut to 2 KB) to the Deployment log. Remove NEW
      with `force=true&v=true`; its anonymous volumes are new and empty. The step fails
      with "web did not become healthy in 41s; kept the previous container". Services
      not yet rolled out are listed in the log as "not deployed" and are left as they
      were.
4. Record the deploy time only when every step succeeded, as today.

When OLD holds more than one Container (a crash in the middle of an earlier rollout can
leave two), all of them count as OLD. Scaling to `len(OLD)+1` and then removing every OLD
Container brings the Service back to one.

**Wait limit** = `start_period + (interval + timeout) × retries + 10s`. Unset fields use
Docker's defaults (interval 30 s, timeout 30 s, retries 3, start_period 0). The limit is
also capped by the Deploy step's existing deadline.

**Cleanup context.** Removing NEW after a failure, and stopping and removing OLD, run on
a context that is not tied to the Deploy step's deadline. Each Engine call gets its own
timeout instead: 30 s, or `StopTimeout` + 30 s for a stop. If the deadline ends the
health wait, the removal of NEW still runs. The log says "kept the previous container"
only when that removal succeeded. If it failed, the log names the leftover container.

**Drift mode** shortens the overlap, because during the overlap the conflicting labels
make Traefik drop the router. The step log says: "web's Traefik labels changed (Domain
edited or zero-downtime just enabled); this deploy has a short outage". The same text
goes in `Deployment.warnings`.

**Cancel** during a rollout step does nothing until the step has finished, the same as
the Deploy step today. The step completes either way, whether it succeeds or rolls back.
Steps that haven't started then stop as they do for a canceled Deployment today.

## Engine port

The stop, remove and inspect calls need a container ID, which `docker compose` can't
take. They go to the Engine API over the socket the panel already dials for stats
(`dockerexec/containersource.go`). They don't use the `docker` CLI: those are three plain
calls, and nothing has to be parsed. `composes` declares the port it consumes, per
ADR-0003:

```go
type ContainerEngine interface {
    ServiceContainers(ctx context.Context, project, service string) ([]EngineContainer, error) // running, non-oneoff
    Inspect(ctx context.Context, id string) (EngineContainerState, error) // status, health, last health output, labels
    Stop(ctx context.Context, id string) error   // no t: container's StopTimeout
    Remove(ctx context.Context, id string, force, volumes bool) error
}
```

`dockerexec` implements it. Tests use a fake.

## API

Base: `/api/projects/{projectID}/environments/{environmentID}/composes/{composeID}`.

- `GET …/zero-downtime` returns
  `[{ "service": "web", "enabled": true, "reasons": [] }]`, one entry per Service in the
  current YAML that has a Domain. `reasons` runs the queue-time checks against the
  current YAML, so the tab can warn before anyone deploys.
- `PUT …/zero-downtime/{service}` with `{ "enabled": true | false }` returns 204. It
  returns 404 when the Compose doesn't exist, and 422 when the Service has no Domain in
  the current YAML. Turning the switch on is allowed even when `reasons` is not empty,
  so the operator can fix the YAML afterwards.
- `POST …/deployments` can answer 422 with the `zeroDowntime` body described in "Checks
  when queueing".

## UI (antd 6.5.1)

Before building, read Ant Design's `llms.txt` and the Switch, Modal, Alert and Typography
pages, as AGENTS.md requires, and follow `docs/GUIDELINES.md`.

- **Domains tab** (`ComposeDomainsTab.tsx`): a "Zero-downtime deploy" card that lists
  each Service with a Domain, one `Switch` per Service. Help text:
  > New containers take traffic only once their healthcheck passes; the old one is then
  > stopped. See the requirements under the YAML editor. Changing this Service's
  > Domains makes the next deploy briefly unavailable.
- If a Service has the switch on and `reasons` is not empty, a warning `Alert` under its
  row lists the reasons, with a link to the Editor tab.
- **Note under the YAML editor** (`ComposeEditorGuide.tsx`, the low-emphasis note
  already there): when at least one Service has the switch on, the note gains a
  "Zero-downtime deploy" block. It lists the requirements, then one line per
  switched-on Service: "web — ready" or "web — has no healthcheck; publishes host port
  8080". Those lines come from `GET …/zero-downtime` and are reloaded after each save.
  The requirements, in this order:
  - a `healthcheck` that passes only once the app can serve requests
  - no `container_name`
  - no fixed host port in `ports:` (`"80"` is fine, `"8080:80"` is not)
  - one replica (no `deploy.replicas` or `scale` above 1)
  - no anonymous volumes: mount a named volume over every path the app writes to,
    including paths the image declares with `VOLUME`
  - the app finishes in-flight requests when it gets SIGTERM, within
    `stop_grace_period`
  - the app tolerates two copies running for a few seconds (no SQLite file shared
    through a named volume, no migrations that lock out the old version)

  The block keeps the note's existing secondary style. A Service that isn't ready shows
  its line as `Text type="warning"`. No `Alert` is added here; the Domains tab's
  `Alert` and the refusal Modal already raise these problems with more weight. When no
  Service has the switch on, the note is exactly as it is today.
- **Deploy refused**: when a deploy returns 422 with `zeroDowntime`, a `Modal` titled
  "Can't deploy with zero downtime" lists `service — reason`. It has **Open editor**
  (Editor tab), **Open domains** (Domains tab) and **Close**. The same modal is used
  wherever a deploy starts: the Compose page's Deploy menu, Redeploy on the Deployments
  tab, and the Project deploy (`project-deploy.ts`). The Project deploy shows the
  refusal for that Compose and carries on with the others, as it does with any other
  refusal.
- The Fresh volumes confirm modal gains its extra line when any Service has the switch
  on.
- The Deployment's step list shows the `rollout:<service>` steps. They are rendered like
  `build:<service>`.

## Error handling

| Situation | Result |
|---|---|
| Queue-time check fails | 422; no Deployment is created |
| Transport file write fails during a deploy | Deploy step fails before any Container changes |
| OLD has an anonymous volume (image `VOLUME`) | step fails before NEW is created; `web` untouched; the log gives the path and the fix |
| `--scale` command fails | step fails; OLD untouched; any Container it did create is found by listing and removed |
| NEW not identified | step fails; every non-OLD Container of the Service is removed |
| NEW unhealthy, exited or over the wait limit | NEW removed (on the cleanup context, even past the deadline), OLD keeps serving, Deployment failed |
| Removing NEW fails | Deployment failed. The log names NEW's ID; OLD and NEW both stay in Traefik until the next deploy counts both as OLD |
| Stop or remove of OLD fails | step fails with the Engine error. NEW is serving, so OLD is left for the next deploy to clear, which counts it as OLD |
| Panel stops mid-rollout | the next deploy finds ≥2 running Containers, counts them all as OLD and converges |
| Transport file deleted by hand | the panel rewrites it at the next startup or deploy. Until then the Domains of Services with the switch on return 404 (spike-proven) |

## Testing

- **domains labels**: switch on → retry middleware and transport label on every Domain
  of the Service, retry last in the chain (after strip, prefix and the user's own
  middlewares), none on the `-http` redirect router. Switch
  off → the generated labels are identical to today's, pinned with a golden test.
- **Checks**: each reason (no healthcheck, `disable`, `NONE`, `container_name`,
  short/long published port, unpublished port allowed, `deploy.replicas` or `scale`
  > 1, an anonymous `volumes:` entry, with named volumes and bind mounts allowed),
  including values that come from `${VAR}`. Drift: a hand-written label with a
  `${VAR}` whose value is unchanged is not drift. Switch on without
  a Domain is ignored. A renamed Service's row is ignored.
- **Snapshot**: the switch set is frozen at enqueue. Toggling after enqueue doesn't
  change the Deployment. An old snapshot without the field deploys with every switch
  off.
- **Rollout sequence** with a fake `ContainerEngine` and a fake runner:
  - argv for the `up` of the other Services and for the `--scale` command
  - healthy path: stop and remove OLD, the 3 s wait without drift
  - drift: no wait, warning recorded
  - unhealthy, exited and timeout paths: NEW removed with volumes, health output in the
    log, later Services not rolled
  - no OLD: plain `up` plus the warning
  - OLD with an anonymous volume mount: the step fails with nothing created; a named
    volume (with the `com.docker.compose.volume` label) or a bind mount passes
  - two OLD Containers: scale to 3, remove both
  - Cancel during a rollout step: the step completes
- **Transport file**: written at startup and on deploy, atomically, and not removed by
  the Certificates reconcile.
- **API**: GET lists only Services with a Domain, with `reasons`. PUT returns 422 without
  a Domain. The deployments POST returns the 422 body.
- **Web**: the switch card, the reasons Alert, and the refusal Modal from the Compose
  page, Redeploy and Project deploy. The editor note: unchanged when no switch is on;
  with a switch on, the requirements list and per-Service lines ("ready" or the
  reasons), reloaded after save; wraps at 320 px.
- **Migration**: the table exists after `00024` and is carried by Server Migration.
- **Manual check on a real install** before merge: rerun the spike's load loop against a
  Domain on the live panel's Traefik for one code-only deploy (expect 0 errors) and one
  deploy after a Domain edit (expect the warning and an outage of about 2 s).

## Resolved questions

- Anonymous volumes block a rollout instead of being kept or deleted. A Container
  created by `--scale` would get empty ones, so the app would lose sight of its data.
  The `volumes:` form is refused at queue time. The image's `VOLUME` is caught at the
  start of the rollout step, before anything is created. Since a Service that rolls out
  has no anonymous volumes, OLD is still removed with `v=false`, and no data is deleted.
- The requirements are also listed in the note under the YAML editor, together with
  each switched-on Service's current status.
- Opt-in per Service, not automatic for every Service with a Domain: apps that can't run
  two copies at once, such as SQLite on a volume or migrations at boot, would otherwise
  break quietly.
- A switched-on Service that can't be rolled is refused before queueing, with a modal.
  It doesn't fall back to a plain deploy.
- An unhealthy new Container: roll back and fail the Deployment.
- The Engine API is used for inspect, stop and remove. Cancel lets a rollout finish.
- The dial timeout is set per Service through the file provider, not as a static flag.
- Label drift: warn and shorten the outage. Versioned router names are deferred.
