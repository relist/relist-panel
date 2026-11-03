# Default log cap on every deployed Service

ADR-0027 capped the Docker logs of the two install containers, but the Services the panel deploys still logged without a limit. Docker's `json-file` driver never rotates on its own, so a long-running Container that writes to stdout eventually fills the host's disk.

**Decision:** On deploy, the panel gives every Service whose YAML has no `logging:` key the same cap as the install containers: `json-file` with `max-size: 10m` and `max-file: 5`, so at most about 50 MB per Container. A Service that writes its own `logging:` is left exactly as written, even when that is only a driver with no options. The cap is fixed. There is no setting and no UI, and the Compose's Preview shows it like the other values the panel adds.

## Considered options

**Set `log-opts` in the host's `/etc/docker/daemon.json` from the install script.** Rejected: it means writing outside the Docker-only footprint, possibly over values the operator already set. It only takes effect after restarting dockerd, which stops every Container on the host, and only for Containers created afterwards. A Migration would not carry it.

**The `local` driver.** Rejected: it compresses its files and rotates by default, but they are not JSON lines, so tools that read `*-json.log` stop working. `json-file` with a cap matches the install containers.

**A panel-wide setting for the size.** Rejected for now: one fixed cap covers the problem, and a Service that needs more can say so in its own `logging:`.

## Consequences

- **The first deploy after this change recreates every Service without `logging:`**, because its configuration changed. Until that deploy, a Container that is already running keeps logging without a cap.
- **`docker compose logs` and the Logs tab keep working**: the driver is still `json-file`. Only the oldest lines are lost once a Container passes 50 MB.
- **Containers the panel does not deploy are not covered**: anything started on the host by hand still uses the daemon's default.
