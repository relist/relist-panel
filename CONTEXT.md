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
One deployable Docker Compose stack (a `docker-compose.yml` edited via Monaco) belonging to exactly one Environment. An Environment can hold multiple Composes, each deployed/stopped independently. Routing is either a **Domain** managed by the panel or Traefik labels the user writes inside the Compose file.
_Avoid_: Stack, App. Never "Service" — see **Service**: that word means one service inside the file, never the whole deployable unit.

**Service**:
One service defined inside a Compose file — the unit the panel starts, stops and monitors. A Service belongs to its Compose; other Composes reach it only by its **Internal hostname**, and only when it is on `relist-network`.
_Avoid_: Container (a Service is the definition; a Container is one running instance of it), Process

**Domain**:
A host and path that Traefik routes to one Service's port in a Compose, managed on the Compose's Domains tab and applied on deploy.
_Avoid_: Route, Hostname (as the entity name)

**Internal hostname**:
A `.internal` alias a Service answers to on `relist-network`, injected on deploy (ADR-0014): the stable `<service>.<docker compose project>.internal` and the readable `<service>.<compose>.<environment>.<project>.internal`. Only Services with a Domain, Traefik labels or an explicit `relist-network` get them.
_Avoid_: Service name (the bare name is ambiguous across Composes), DNS name

**Container**:
One running instance of a Service, created by a deploy. Its identity is not stable: every deploy mints a new one, so anything that outlives a single deploy — metric history, for one — is keyed by Service, never by Container.
_Avoid_: Instance

**Image**:
A Docker image present on the host, listed and loaded from the Images page. A Compose references one by name in its file; the panel never builds or pulls one itself.
_Avoid_: Build, Artifact

**Terminal Session**:
One interactive shell in one Container of a Service, opened from the Compose page's Terminal tab and living only as long as that page. _Avoid_: Console, SSH.

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
A key-value pair scoped to an Environment, interpolated into all of that Environment's Composes at deploy time, unless a Compose Variable of the same key overrides it. A variable may be flagged **secret**, which masks its value on screen — a display tier owned by the user, not a security boundary: the value is stored and served as any other, and the panel's single admin can always reveal it.
_Avoid_: treating the secret flag as an encryption or access tier; "plain" is not a domain word, just the tag for the flag's absence.

**Compose Variable**:
A key-value pair scoped to one Compose. It overrides an Environment Variable of the same key for that Compose only, and is interpolated into the Compose's YAML like one. It has the same secret flag.
_Avoid_: Local variable, Override (the overriding is a behaviour, not the entity).

**Variable Set**:
A named list of key-value pairs belonging to an Environment, delivered to the containers of the Services it is bound to and to no others. Never interpolated into YAML.
_Avoid_: Env, Env group, Env file (the file is how it is delivered, not what it is).

**Binding**:
The link between one Service in a Compose and one Variable Set. Ordered per Service.
_Avoid_: Attachment, Mount.

**Two-factor**:
A time-based one-time code (TOTP) the admin account may require at sign-in, on top of its password. Opt-in from the Account page. Recovery is deliberately the host: `relist-panel reset-2fa`, run inside the panel container, clears it — anyone who can run that already owns the Docker host.
_Avoid_: MFA, OTP (as the feature name), 2FA in code identifiers — use `twoFactor` / `totp`.
