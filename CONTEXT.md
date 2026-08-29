# Relist Panel

A minimal self-hosted web UI for managing Docker Compose deployments on a single Docker host, using Traefik as the reverse-proxy gateway. Scoped to raw Compose files only — no git-based app builds, no database-as-a-service, no multi-host support.

## Language

**Project**:
A named grouping of Environments that belong to the same application or system (e.g. "my-saas"). Holds only a name and description.
_Avoid_: App, Application (this system has no separate "Application" entity type)

**Environment**:
A named deployment target within a Project (e.g. "production", "staging"). Holds a name and its own set of Environment Variables, injected into its Composes at deploy time. Has no domain or server field of its own.
_Avoid_: Stage, Env (as a distinct entity name)

**Compose**:
One deployable Docker Compose stack (a `docker-compose.yml` edited via Monaco) belonging to exactly one Environment. An Environment can hold multiple Composes, each deployed/stopped independently. Traefik routing is configured by the user as labels inside the Compose file — the panel has no separate domain-management UI.
_Avoid_: Stack, Service (a Compose file already contains its own internal "services" — don't reuse that word for the whole deployable unit), App

**Environment Variable**:
A key-value pair scoped to an Environment, interpolated into all of that Environment's Composes at deploy time.
_Avoid_: Secret (no distinct secret-handling tier exists yet)
