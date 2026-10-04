Status: ready-for-agent

# Relist Panel v1 — Docker Compose management panel

## Problem Statement

An operator running self-built services on a single Docker host has no lightweight way to manage what's deployed. The existing options either lock them into Docker Swarm/Kubernetes (more orchestration than a single host needs), or pull in a full PaaS like Dokploy/Coolify with git-based app builds, database-as-a-service, teams, and other machinery the operator doesn't want to run or maintain. They just want to write a `docker-compose.yml`, deploy it, see whether it's running, and have Traefik route to it — through a minimal web UI instead of SSH-ing in and typing `docker compose` commands by hand every time.

## Solution

A minimal, self-hosted web panel that manages Docker Compose deployments on a single Docker host, installed via its own `docker compose` bootstrap (which also brings up Traefik and a shared network). The panel organizes work as **Project → Environment → Compose**: a Project groups the Environments of one application, an Environment (e.g. "production") carries its own Environment Variables, and a Compose is one `docker-compose.yml` — edited directly in a Monaco editor, similar to Dokploy's compose screen — that the operator can deploy, stop, restart, or delete. The panel shells out to the real `docker compose` CLI over the host's mounted Docker socket, auto-joins every deployed Compose to the shared Traefik network so routing works without extra setup, and otherwise stays out of the way: Traefik labels, service definitions, and everything else in the YAML are exactly what the operator wrote.

## User Stories

**Installation & runtime**

1. As an operator, I want to install the whole panel (panel + Traefik + shared network) with a single `docker compose up`, so that I don't need to provision Traefik or networking separately.
2. As an operator, I want the panel's data directory bind-mounted at an identical path on the host and inside the panel container, so that Compose files I deploy through the panel can use relative build contexts and bind volumes correctly.
3. As an operator, I want the data directory to default to `/var/lib/relist-panel` but be overridable via an environment variable, so that I can place it on a different disk if needed.
4. As an operator, I want the panel to ship as a single Go binary with the frontend embedded, so that upgrading is replacing one binary/image, with no separate frontend deployment step.

**Auth**

5. As an operator, I want to create the single admin account on first run, so that only I can access the management UI.
6. As an operator, I want to log in and stay logged in across browser reloads, so I don't have to re-authenticate constantly.

**Projects**

7. As an operator, I want to create a Project with a name and description, so that I can group the Environments of one application under it.
8. As an operator, I want to see a list of all Projects, so that I can navigate to the one I need.
9. As an operator, I want to edit a Project's name or description, so that I can keep its metadata current.
10. As an operator, I want to delete a Project — cascading to its Environments and Composes, stopping and removing their containers first — so that I can clean up an application I no longer run.

**Environments**

11. As an operator, I want to create an Environment within a Project with a name, so that I can represent a deployment target like "production" or "staging".
12. As an operator, I want a Project's first Environment to default to "production", so that the common single-environment case needs no extra naming decision.
13. As an operator, I want to see all Environments under a Project, so that I can pick the one I want to manage.
14. As an operator, I want to define Environment Variables (key-value pairs) scoped to an Environment, so that I can share config across every Compose in that Environment without repeating it in each Compose file.
15. As an operator, I want to edit or delete an existing Environment Variable, so that I can rotate or remove config values.
16. As an operator, I want to delete an Environment — cascading to its Composes, stopping and removing their containers first — so that I can retire a deployment target.

**Composes**

17. As an operator, I want to create a new Compose within an Environment by writing/pasting a `docker-compose.yml` in a Monaco editor, so that I can define exactly what gets deployed.
18. As an operator, I want YAML syntax highlighting in the editor, so that I can spot structural mistakes before deploying.
19. As an operator, I want to edit an existing Compose's YAML and save my changes, so that I can iterate on a running deployment.
20. As an operator, I want each Compose to have its own name within its Environment, so that I can identify it and so the panel can build a unique deployment identity for it.
21. As an operator, I want to see all Composes within an Environment along with their current status (e.g. "3/3 running", "stopped"), so that I can tell at a glance what's deployed.

**Deploy lifecycle**

22. As an operator, I want to click "Deploy" on a Compose to bring it up, so that my saved YAML becomes running containers.
23. As an operator, I want the panel to automatically join every service in my Compose to the shared Traefik network before deploying, so that Traefik can route to my containers without me declaring that network myself every time.
24. As an operator, I want my Environment's Environment Variables injected into the Compose at deploy time, so that my services pick up shared config without me hardcoding it into the YAML.
25. As an operator, I want to write my own Traefik labels directly in the Compose YAML, so that I control routing exactly, without a separate domain-management UI getting in the way.
26. As an operator, I want to click "Stop" on a deployed Compose to bring its containers down without deleting my saved YAML, so that I can pause a deployment temporarily.
27. As an operator, I want to click "Restart" on a Compose, so that I can cycle its containers without a full redeploy.
28. As an operator, I want to click "Delete" on a Compose to stop and remove its containers, networks, and volumes and delete its files from disk, so that I can fully tear down something I no longer need.
29. As an operator, I want every Compose to deploy under a deterministic, immutable unique identity (database IDs for new Composes; frozen names for legacy rows, per ADR-0011), so that two Composes never collide on container or network names.

**Status & logs**

30. As an operator, I want to refresh a Compose's status on demand as well as see it on page load, so that I know whether it's actually running without needing constant background polling.
31. As an operator, I want to view the last ~200 lines of a Compose's logs on demand, so that I can debug why a deployment isn't behaving as expected.

## Implementation Decisions

**Domain model** (see `CONTEXT.md`): Project (name, description) has many Environments; Environment (name) has many Environment Variables (key/value) and many Composes; Compose (name, YAML content) belongs to exactly one Environment.

**Stack** (ADR-0001): Go backend; frontend is Vite + React + TypeScript + Tailwind, built to static assets and embedded into the Go binary via `go:embed`. Metadata storage is SQLite via a pure-Go (no-cgo) driver, so the binary stays statically linkable. Ships as one binary / one minimal Docker image.

**Go architecture** (ADR-0003): package-per-domain (`project`, `environment`, `compose`, `auth`), not per-layer. Exactly one interface in the whole system — `ComposeRunner` (`Up`/`Down`/`Restart`/`Status`/`Logs`) — declared in the `compose` package and implemented for real in a separate `dockerexec` package that shells out to the `docker compose` CLI; wired together only in the composition root. SQLite is accessed directly via hand-written `database/sql` repository methods per domain package — no ORM, no codegen. HTTP routing uses the stdlib `net/http.ServeMux` (Go 1.22+ patterns), no external router. Config is a small hand-rolled struct from `os.Getenv`. Schema migrations use goose, with `.sql` files embedded via `go:embed` and run automatically on startup. The frontend build output is embedded from its own `webassets` package.

**Docker execution & networking** (ADR-0002):
- The panel controls the host's Docker Engine by shelling out to the `docker compose` CLI plugin over `/var/run/docker.sock`, mounted into the panel container.
- The install bootstrap (`docker-compose.yml` shipped with the panel) brings up the panel and Traefik together, plus a shared external network (e.g. `relist-public`), which the panel ensures exists on startup.
- Before deploying any user Compose, the panel parses its YAML and injects a reference to the shared network into every service (merging with any `networks:` block the user already declared), so Traefik can route to it without the user wiring that up by hand. This is the one place the panel modifies user-authored YAML — everything else (labels, service definitions, restart policies, etc.) passes through untouched.
- Environment Variables are passed to `docker compose` via a generated env file, not injected into the YAML text itself.
- Every Compose deploys under a deterministic Compose project name derived from its Project, Environment, and Compose names, passed via `docker compose -p`, so deployments never collide on container/network names.
- The panel's data directory is bind-mounted at an **identical path** on the host and inside the panel container (default `/var/lib/relist-panel`, overridable via an environment variable) — not a named Docker volume — because `docker compose up` executes against the host's dockerd, so relative paths in a user's Compose file (build context, bind volumes) only resolve correctly if that path exists identically on both sides. New Composes' resolved files live under `.composes/<ID>/`; legacy Composes keep their per-Project/per-Environment/per-Compose directory (ADR-0011); the panel's SQLite database lives alongside them in the same directory.

**Auth**: single admin account, created on first run when none exists; session persisted via a cookie/token. No teams, roles, or multi-user support.

**Frontend UI** (ADR-0004): Ant Design is the component library, integrated with Tailwind via the "compatible-style" `@layer` pattern (`StyleProvider layer` + `ConfigProvider`) so Tailwind utilities can override antd component styles without specificity conflicts. `DESIGN.md` documents Ant Design's default v6 light-theme tokens as the starting design system.

Navigation mirrors the domain model 1:1, via `Layout` + `Sider` + `Menu`: a Project list is the landing page; a Project's page shows its Environments as `Tabs` (defaulting to "production"); an Environment's page shows its Composes as a list/`Table` with a status badge per Compose, plus a separate tab for editing that Environment's Environment Variables. A Compose's own detail page has exactly two `Tabs`: **General** (the Monaco editor for its YAML, the Deploy/Stop/Restart/Delete actions, and its status badge) and **Logs** (on-demand fetch of recent log lines). Environment Variables are edited once per Environment, not repeated per Compose, since that's where they're scoped in the domain model.

Theming ships light-only in v1 via antd's `ConfigProvider` `defaultAlgorithm` — no dark mode toggle yet, though the token-based setup makes adding one later a small change rather than a rewrite.

## Testing Decisions

Tests should only exercise external behavior, not internal function calls — this repo has no prior test suite to follow, so this spec establishes the pattern for everything after it.

- **Primary and only seam: the HTTP API.** Tests drive the Go server through real HTTP requests (`httptest`) backed by a real SQLite database (temp file or in-memory). This covers all Project/Environment/Compose/Environment-Variable CRUD and all deploy-lifecycle orchestration logic (naming, path construction, network injection, env-file generation) end to end.
- **`ComposeRunner` is faked in tests.** The real implementation shells out to `docker compose`; the fake records what it was asked to do (project name, resolved YAML, env file contents) so tests can assert on exactly what would have been executed, without a real Docker daemon and without flakiness.
- Tests for each domain package are written as external test packages (e.g. `compose_test`), so they can only call exported behavior — enforcing the HTTP seam rather than reaching into internals.
- No automated frontend tests in v1 — manual verification only, consistent with the minimal v1 scope.

## Out of Scope

- No separate git-based "Application" type — users bring pre-built images in their Compose file.
- No database-as-a-service.
- No git webhook / auto-deploy — deploys are manual via a "Save & Deploy" action.
- No domain-management UI — Traefik labels are written by hand inside the Compose file.
- No multi-user/team/role system — single admin account only.
- No Compose version history or rollback — editing overwrites the live file.
- No multi-host / remote Docker support — single host only.
- No live log streaming or websockets — logs are fetched on demand.
- No automatic background status polling — status refreshes on page load and on manual refresh only.
- No ORM, query-builder codegen (sqlc), external HTTP router, or config-loading library — stdlib and hand-written code throughout, per ADR-0003.
- No Clean Architecture or full Hexagonal layering — one interface (`ComposeRunner`) exists where it's actually needed; everything else is used concretely.
- No dark mode toggle in v1 — ships light theme only (ADR-0004); trivial to add later via antd's algorithm switch, so deliberately deferred rather than built now.

## Further Notes

- Full architectural reasoning lives in `docs/adr/0001-v1-scope-and-stack.md`, `docs/adr/0002-docker-execution-networking-storage.md`, `docs/adr/0003-go-architecture.md`, and `docs/adr/0004-frontend-ui-library.md` — read them before implementing any part of this spec. `DESIGN.md` documents the Ant Design tokens the UI is built from.
- `CONTEXT.md` defines the Project/Environment/Compose/Environment Variable vocabulary; use these exact terms in tickets, code, and tests.
- The shared Traefik network and the identical-host-path data directory are the two non-obvious "gotchas" a future contributor needs to understand before touching deploy logic — both are explained in ADR-0002.
- When this spec is split into tickets, the domain packages (`project`, `environment`, `compose` + `dockerexec`, `auth`) are natural ticket boundaries, roughly following the order they appear in the user stories above (a Project/Environment CRUD slice can ship before the deploy-lifecycle slice needs `dockerexec` to exist).
