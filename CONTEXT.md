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
_Avoid_: Stack, App. Never "Service" — see **Service**: that word means one service inside the file, never the whole deployable unit.

**Service**:
One service defined inside a Compose file — the unit the panel starts, stops and monitors. A Service belongs to its Compose and is not addressable outside it.
_Avoid_: Container (a Service is the definition; a Container is one running instance of it), Process

**Container**:
One running instance of a Service, created by a deploy. Its identity is not stable: every deploy mints a new one, so anything that outlives a single deploy — metric history, for one — is keyed by Service, never by Container.
_Avoid_: Instance

**Volume**:
One entry in a Compose's `volumes:` list, attached to one of its Services. Either a named Docker volume or a host path (a bind mount) — the panel treats both alike, because the operator declares both in the same place.
_Avoid_: Mount, Storage. Docker's own "volume" means only the named kind, so never assume a Volume is a named volume.

**Volume Explorer**:
The screen inside the panel where a Volume's files are listed, edited, uploaded and downloaded. Part of the panel, not a separate application.
_Avoid_: File Manager, File Browser

**Orphan Volume**:
A named Docker volume that Compose created for a Compose's project but whose YAML no longer declares. Precisely: it carries this Compose's `com.docker.compose.project` label and a non-empty `com.docker.compose.volume` label, its name is exactly `<project>_<logical>`, and it is not a named Volume in the Compose resolved with every profile enabled. Volumes with an explicit `name:`, `external: true` volumes, anonymous volumes and bind mounts are never Orphan Volumes. The panel only deletes one on the operator's request, and never forces it.
_Avoid_: Dangling volume — that is Docker's term for any unreferenced volume, whichever project made it.

**Environment Variable**:
A key-value pair scoped to an Environment, interpolated into all of that Environment's Composes at deploy time. A variable may be flagged **secret**, which masks its value on screen — a display tier owned by the user, not a security boundary: the value is stored and served as any other, and the panel's single admin can always reveal it.
_Avoid_: treating the secret flag as an encryption or access tier; "plain" is not a domain word, just the tag for the flag's absence.

**Two-factor**:
A time-based one-time code (TOTP) the admin account may require at sign-in, on top of its password. Opt-in from the Account page. Recovery is deliberately the host: `relist-panel reset-2fa`, run inside the panel container, clears it — anyone who can run that already owns the Docker host.
_Avoid_: MFA, OTP (as the feature name), 2FA in code identifiers — use `twoFactor` / `totp`.
