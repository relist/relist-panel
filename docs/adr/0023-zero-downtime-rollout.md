# Zero-downtime rollout for Services with a Domain

A Deployment runs `docker compose up`, which stops a changed Service's Container and then starts its replacement. A Service behind a Domain answers 502 from Traefik for that gap, which is every deploy of a web app.

**Decision:** a per-Service switch, available only to Services with a Domain, turns that Service's part of a Deployment into a **Rollout**. The rest of the Compose deploys first with `up --no-deps` minus the switched-on Services; then each of them, in YAML order, runs `up -d --no-deps --no-recreate --scale <svc>=N+1 <svc>`, waits for the new Container's healthcheck to pass, and stops and removes the old Container by ID through the Engine API. A new Container that is not healthy in time is removed instead and the Deployment fails, leaving the old one serving. Fresh volumes and a first deploy run a plain `up`.

For each Domain of a switched-on Service, the label injection of ADR-0013 adds two things: a `retry` middleware with `attempts=4`, last in the router's chain, and `loadbalancer.serversTransport=relist-fastdial@file`. The transport, `dialTimeout: 1s`, lives in `<data dir>/.traefik/dynamic/relist-transports.yml`, in the file-provider directory of ADR-0022. The panel writes that file at startup and before any deploy that uses it, and never deletes it.

A Service whose switch is on is checked when the Deployment is queued, against `docker compose config` of the snapshot's own files. No healthcheck, a `container_name`, a fixed host port, more than one replica, or an anonymous volume refuses the deploy with 422, naming each Service and reason. A Deployment's switched-on Services are part of its snapshot (ADR-0017).

## Evidence

Spikes on Docker Compose v5.5.1 and Traefik v3.7, without Swarm:

- `--scale` with `--no-recreate` creates one Container from the current file and leaves the old one alone. 20+ rollouts in a row left one Container each time; a later plain `up` did not recreate it.
- Traefik's docker provider skips a Container whose health is `starting`: no request reached the new Container before it was healthy.
- Stopping the old Container failed a request in 3 of 4 rollouts until Traefik rebuilt its config. With Traefik's default 30 s dial timeout a dial to the dead Container hung 14–44 s. With `dialTimeout: 1s` and `attempts=4`: 180/180 OK across POST, GET and PUT, after both `docker stop` and `docker kill`; the slowest request took about 3 s.
- The retry never sends a request twice. Killing a Container mid-request (every method, twice each) gave the client a 502 and the app the request once: Traefik retries only a failed dial.
- Two Containers whose Traefik labels differ for the same router make Traefik drop the router: 404 for the whole overlap. Stopping the old Container as soon as the new one is healthy cut that to 1.9–2.0 s (3 runs).
- A missing transport file disables every Traefik service that names it: its Domains return 404 until the file is back.

## Considered options

**Versioned router names, so old and new labels never conflict.** Rejected: every deploy would leave Traefik two routers for one Domain, picked by priority, and hand-written labels could not follow.

**A static `--serversTransport.forwardingTimeouts.dialTimeout`.** Rejected: it changes every route on the host and needs an edit to the install file. The per-Service transport touches only switched-on Services.

**Swarm rolling updates.** Rejected: the panel runs Compose without Swarm, and Swarm changes networking, volumes and secrets for every Compose.

**Blue/green under a second project name.** Rejected: a second project name gets its own, empty named volumes, and ADR-0005 freezes the project name.

**`--scale <svc>=1` to drop the old Container.** Rejected: Compose chooses which Container to remove. The panel removes the old one by ID.

**`docker pause` on the old Container to drain it.** Rejected: Traefik kept routing to the paused Container.

## Consequences

- **The healthcheck has to mean "ready"**, and the app has to finish in-flight requests on SIGTERM within `stop_grace_period`. The panel cannot check either; the switch's confirm modal and the note under the YAML editor say so.
- **A deploy that changes the Service's Traefik labels has a short outage** (about 2 s of 404), logged as a warning: an edited Domain, the first deploy after the switch is turned on, or edited hand-written `traefik.*` labels. The old Container is stopped as soon as the new one is healthy.
- **Other Services still restart** as before; only switched-on Services roll out.
- **Two copies of the Service run for a few seconds.** An app that cannot tolerate that (a SQLite file on a named volume, a migration that locks out the old version) must keep the switch off.
- **Cancel does not interrupt a Rollout**: it finishes, like the rest of the apply phase.
- **An install older than ADR-0022's file provider** turns a switched-on Service's Domains into 404 after deploy, because the transport is never loaded. The README's upgrade section is the guard.
- Extends ADR-0013 (label injection) and ADR-0017 (execution snapshot).
- Spec: `.scratch/zero-downtime-deploy/spec.md`.
