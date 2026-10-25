# Sources and a host-wide deployment queue

ADR-0001 kept git-based builds out of scope: a Compose ran only images that already existed on the host, and the glossary said the panel never builds one. Operators whose code lives in a repository had to build elsewhere and load the image through the Images page before every deploy. Deploys also ran synchronously inside one HTTP request holding the Project gate, which suits `up` (seconds) but not a build (minutes).

**Decision:** a Compose may have one **Source**, either a Git repository fetched over SSH with a per-Compose **Deploy key** the panel generates, or an uploaded zip. Either way its code lives in `<compose dir>/src/`. This supersedes ADR-0001's "No separate git-based Application type" non-goal. A Source is optional and belongs to a Compose. It is not a new Application entity.

- **What to build is written in the YAML, as Compose's own `build:`** (for example `context: ./src/api`). The panel offers to insert those blocks from the Dockerfiles it finds, into the editor draft, and never injects or locks them. Rejected: a panel-managed build-target table injected at deploy (the ADR-0013 pattern). It would duplicate the YAML in a second table, and the file the operator reads would not be the file that runs.
- **Every deploy is a Deployment in one host-wide queue that runs one at a time.** Builds and `up` share that queue, so a build delays other Composes' deploys. The operator chose a host that runs only one heavy thing at a time over the throughput of separate build and deploy lanes.
- **Services build one at a time**, with `docker compose build <service>` in YAML order. That is followed by `up --no-build`, because `up --build` would rebuild everything again in parallel. A Compose without a Source keeps `up --build`, so existing YAML is unaffected.
- **Fetch and build run outside the Project gate.** They touch only `./src` and images. Only the Deploy step takes the gate.
- **No auto-pruning.** Rebuilt images become dangling and are left for the operator to prune.

## Consequences

- `POST /deploy` becomes `POST /deployments` returning `202`. Deploy warnings move from the response into the Deployment's log and record.
- A panel restart fails the Deployment that was running and resumes the queued ones.
- The Deploy key's private half is stored in the database. Like the secret flag, this is not a security boundary: the single admin owns the host.
- Build args and build secrets use Compose's `build.args` and `build.secrets` (`environment:` source), interpolated from the Compose's `--env-file`. A probe on 2026-10-11 confirmed both with `docker:27-cli`, which already ships `git` and `ssh`.
- Spec: `.scratch/source-builds/spec.md`.
- Superseded in part by ADR-0020 (SSH key library).
