# V1 scope: raw Compose passthrough, Go/Vite stack

Relist Panel v1 manages only Projects → Environments → raw Compose stacks, edited directly by the user in a Monaco editor. We deliberately left out several features Dokploy/Coolify ship, and picked Go over the originally-recommended Next.js, because the goal is a small, single-binary panel rather than feature parity with those tools.

## Explicit non-goals (v1)

- No separate git-based "Application" type — users bring pre-built images in their Compose file. (Widened since: the panel now lists the host's images and loads `docker save` archives from the Images page — `.scratch/image-upload/spec.md` — while git builds and registries remain out of scope.)
- No database-as-a-service.
- No git webhook / auto-deploy — deploys are manual via "Save & Deploy".
- No domain-management UI — Traefik labels are written by hand inside the Compose file. (Widened since: the panel now manages a Compose's domains on its Domains tab and injects the Traefik labels at deploy — ADR-0013.)
- No multi-user/team/role system — single admin account only.
- No Compose version history or rollback — editing overwrites the live file.

## Stack

Backend: Go. Frontend: Vite + React + TypeScript + Tailwind, built to static files and embedded into the Go binary via `go:embed` — ships as one binary / one minimal Docker image, no Node runtime in production. Metadata storage: SQLite via a pure-Go (no-cgo) driver, so the binary stays statically linkable.
