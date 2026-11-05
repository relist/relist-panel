# Relist Panel

A minimal self-hosted web UI for managing Docker Compose deployments on a single Docker host, using Traefik as the reverse-proxy gateway. Scoped to Compose files — a Compose may build its images from a Source (ADR-0016), but there is no separate Application type, no database-as-a-service and no multi-host support.

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

**Certificate**:
A TLS certificate and its private key, uploaded by the operator to the host-wide Certificates page and handed to Traefik's file provider. Traefik serves it to any HTTPS Domain on provider None whose host it covers, chosen by SNI; the panel does not link a Certificate to a Domain (ADR-0022).
_Avoid_: SSL, Cert (as the entity name), Certificate resolver (that is how Traefik obtains certificates, e.g. Let's Encrypt)

**Access log**:
The record of every HTTP request Traefik receives, kept as JSON lines in `<data dir>/.traefik/access/`, one gzipped file per day, and deleted by the panel after the retention period. It is host-wide, not per Compose (ADR-0027).
_Avoid_: Request log, Traffic log (as the entity name). "Traefik log" means Traefik's own log on stdout, not this.

**Traefik override**:
The operator's part of Traefik's static config, `<data dir>/.traefik/static/traefik.override.yml`, edited on the Traefik page. The panel merges it over the base into the generated `traefik.yml` that Traefik reads (ADR-0030).
_Avoid_: Traefik config (ambiguous between base, override and generated file), traefik.yml (that is the generated file).

**Traefik apply**:
Writing a new Traefik override and restarting Traefik with self-heal: validate, dry-run, back up, write, restart, watch for 60 s, and roll back on failure (ADR-0030).
_Avoid_: Reload (Traefik cannot reload static config; this is a restart).

**Internal hostname**:
A `.internal` alias a Service answers to on `relist-network`, injected on deploy (ADR-0014): the stable `<service>.<docker compose project>.internal` and the readable `<service>.<compose>.<environment>.<project>.internal`. Only Services with a Domain, Traefik labels or an explicit `relist-network` get them.
_Avoid_: Service name (the bare name is ambiguous across Composes), DNS name

**Container**:
One running instance of a Service, created by a deploy. Its identity is not stable: every deploy mints a new one, so anything that outlives a single deploy — metric history, for one — is keyed by Service, never by Container.
_Avoid_: Instance

**Image**:
A Docker image present on the host, listed and loaded from the Images page. A Compose references one by name in its file, or builds one from its Source; the panel builds images only from a Source and never pulls one itself — except the Scanner's own image (ADR-0021), the panel's own image for a Panel update (ADR-0024) and the BuildKit image Builders run (ADR-0028).
_Avoid_: Build, Artifact

**Builder**:
A named, resource-limited place where the panel builds Images: one BuildKit container and its cache, with fixed CPU, memory and max parallel steps. **Docker default** is the built-in, unlimited Builder (dockerd's own). Each Compose uses the panel default Builder unless it picks one (ADR-0028).
_Avoid_: Runner, Worker, Build agent

**Scanner**:
The vulnerability scanner the panel runs (Trivy), as a short-lived, locked-down container from the Scanner image, which is named by digest. It never holds the Docker socket (ADR-0021).
_Avoid_: Scout, Agent

**Vulnerability scan**:
One run of the Scanner over one Image, keyed by image ID. Only the latest result per Image is kept. Scans run one at a time in the host-wide scan queue, which never runs while a Deployment is running.
_Avoid_: Job, Audit

**Finding**:
One CVE matched against one package in one Image: severity, installed version, fixed version. A Finding is *fixable* when it has a fixed version.
_Avoid_: Issue, Alert

**Source**:
The source code of one Compose, from a Git repository or an uploaded zip, kept in `<compose dir>/src/`. A Compose has at most one. A Git Source chooses an SSH key, and may check out only some directories (its sparse paths) plus the files at the repository root. What to build from it is the Compose file's own `build:` (ADR-0016).
_Avoid_: Repo (a zip Source has none), App, Application

**SSH key**:
An SSH key pair the panel generates and keeps in the host-wide SSH keys library. A Git Source chooses one; one key may serve many Composes. The operator adds its public half to each repository as a read-only deploy key (ADR-0020).
_Avoid_: Credential, Deploy key (the per-Compose key of ADR-0016)

**Deployment**:
One queued run that brings a Compose's Containers up to date: fetch the Source, build each Service that has `build:` one at a time on the Compose's Builder, then `up`. A newly queued Deployment keeps the configuration selected when it was queued, independently of later edits. Numbered per Compose, with status, steps and a log. All Deployments on the host share one queue and run one at a time.
_Avoid_: Build (the build is one step of a Deployment), Job, Release

**Zero-downtime deploy**:
A per-Service setting, available to Services with a Domain. When it is on, a Deployment replaces the Service's Container by **Rollout** instead of recreating it (ADR-0023).
_Avoid_: Rolling update (Swarm's feature), Blue/green.

**Rollout**:
The step of a Deployment that starts a new Container of one Service next to the old one, waits until it is healthy, then stops and removes the old one. If the new one is not healthy in time, it is removed instead and the Deployment fails.
_Avoid_: Update, Swap.

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
A key-value pair scoped to an Environment, interpolated into all of that Environment's Composes at deploy time, unless a Compose Variable of the same key overrides it. A variable may be flagged **secret**: a User below Editor in that Environment never receives its value, and for an Editor or an Admin it stays a display tier that masks the value on screen. It is not encryption: the value is stored as any other.
_Avoid_: treating the secret flag as encryption; "plain" is not a domain word, just the tag for the flag's absence.

**Compose Variable**:
A key-value pair scoped to one Compose. It overrides an Environment Variable of the same key for that Compose only, and is interpolated into the Compose's YAML like one. It has the same secret flag.
_Avoid_: Local variable, Override (the overriding is a behaviour, not the entity).

**Variable Set**:
A named list of key-value pairs belonging to one Compose, delivered to the containers of the Services it is bound to and to no others. Never interpolated into YAML.
_Avoid_: Env, Env group, Env file (the file is how it is delivered, not what it is).

**Binding**:
The link between one Service in a Compose and one Variable Set. Ordered per Service.
_Avoid_: Attachment, Mount.

**Two-factor**:
A time-based one-time code (TOTP) a User may require at sign-in, on top of their password. Opt-in from the Account page, per User. An admin's reset link also turns it off. Recovery is deliberately the host: `relist-panel reset-2fa`, run inside the panel container, clears it — anyone who can run that already owns the Docker host.
_Avoid_: MFA, OTP (as the feature name), 2FA in code identifiers — use `twoFactor` / `totp`.

**User**:
One person who signs in to the panel, with their own email, password and Two-factor. A User is **invited** (no password yet), **active** or **disabled** (signed out and refused at sign-in). A User who is not an Admin sees only the Projects and Environments their Grants reach, and does there what their Role allows (ADR-0031).
_Avoid_: Account (the Account page is where a User edits their own sign-in), Member, Operator (as the name for a person).

**Owner**:
The User with id 1, made by `relist-panel create-admin`. Always an Admin: the web can never disable, delete or demote it, and only the Owner can send the Owner a reset link. There is no transfer; a new owner takes over the id 1 account.
_Avoid_: Root, Superuser.

**Admin**:
A User who sees every Project and may use every screen of the panel, the host-wide pages and the Users page included.
_Avoid_: Administrator (spell it Admin), Staff.

**Role**:
Viewer, Operator or Editor: what a User may do in an Environment. A Viewer reads, an Operator also deploys, restarts and stops, an Editor also changes the Compose and its settings and reads secret values. A **Project Editor** holds Editor on the Project itself and may also edit the Project and add or delete its Environments.
_Avoid_: Permission (a Role is the unit; there are no separate permissions).

**Grant**:
A Role given to one User on one Project, or on one Environment, which may also be **No access** to hide it. The Environment Grant wins over the Project Grant. Admins need none.
_Avoid_: Membership, ACL.

**Audit event**:
One recorded action: who, when, what, where, and whether it was done or refused. Changes, refusals, sensitive reads (secret values, Volume downloads, a Terminal session) and sign-ins are recorded; a variable's value never is. Admins read all of them, a Project's Editors that Project's.
_Avoid_: Log entry (the Access log and container logs are other things), History (Deployment history is another thing).

**Compose policy**:
The rules a Compose's resolved config must meet to deploy without an Approval: nothing that reaches the host (binds outside its directory and the bind roots, the Docker socket, privileged, host namespaces) or another Compose's volumes.
_Avoid_: Security policy, Sandbox.

**Approval**:
An approver's consent to deploy one resolved config of one Compose, kept as that config's hash; any change to the config, a variable value included, needs a new one. The approver is every Admin, or only the Owner.
_Avoid_: Review, Sign-off.

**Invite link**:
A single-use link with which a User sets their password: an invite for a new User (7 days) or a reset for an existing one (24 hours). The panel stores only its hash and shows it once; an Admin hands it over, the panel sends no email.
_Avoid_: Magic link, Token (the token is part of the link), Invitation email.

**Migration**:
Moving everything one panel manages to an empty panel on another host, which pulls it from the old one over HTTPS (ADR-0018). Everything moves; the operator chooses only which host paths outside the data directory to include.
_Avoid_: Backup, Restore, Transfer (as the feature name); Source for the old panel (a Source is a Compose's code), say old host or old panel.

**Migration token**:
A short-lived, single-use secret created on the old panel that lets one new panel claim it for a Migration. Lives 1 hour, 24 hours or 7 days.
_Avoid_: API key, Pairing code.

**Migration lock**:
The persisted state in which the old panel refuses every change and keeps its Composes stopped, so what is copied stays what is there. It survives a restart. After the Migration it remains as **Migrated** until the operator resumes that server.
_Avoid_: Maintenance mode (the maintenance page is what visitors of its Domains see, not the lock).

**Control socket**:
The Unix socket inside the panel container through which the `relist-panel` command talks to the running panel. Reaching it requires running a command in the container.
_Avoid_: admin socket, API socket.

**Install**:
The Docker Compose project on the host that runs the panel and Traefik: its directory, its Install file, its Install override and its `.env`. The panel finds it from its own Container's Compose labels.
_Avoid_: Stack, Panel compose (a Compose is something the panel deploys, never the panel itself)

**Install script**:
The `install.sh` attached to each release, run as `curl … | sh`, that sets up a new Install on a host with Docker, or runs a Panel update with `--update` (ADR-0025).
_Avoid_: Installer image, Bootstrap, Setup wizard

**Install file**:
The Install's `docker-compose.yml`, owned by the panel: each version ships one, and a Panel update replaces or merges it.
_Avoid_: Base file, Main compose

**Install override**:
The Install's `docker-compose.override.yml`, owned by the operator, where the Install is customised. A Panel update never writes it. The **Traefik override** is the equivalent for Traefik's static config.
_Avoid_: Custom compose, Local file

**Panel update**:
One run, started from the web or from the Install script's `--update`, that moves the Install to a newer panel version and puts the old one back if the new one is not healthy (ADR-0024, ADR-0025).
_Avoid_: Upgrade, Release, Deployment (that deploys a Compose), Migration (that moves the panel to another host)

**Update manifest**:
The signed description of one panel version: its image digest, its Install file, its Install invariants and the manual steps it needs.
_Avoid_: Release notes, Changelog

**Release candidate**:
A panel version `X.Y.Z-rc.N`, published as a GitHub pre-release and reached only with `install.sh --update --version` (ADR-0026).
_Avoid_: Beta, Pre-release channel

**Install invariant**:
A property the Install's merged configuration must have for a panel version to run, such as the Docker socket mount on the panel Service. Each Update manifest lists its own.
_Avoid_: Rule, Requirement

**Updater**:
The short-lived Container, of the current panel image, that runs `relist-panel update`: it checks a Panel update, asks the panel to quiesce, backs up, applies and rolls back. It runs outside the Install so it outlives the panel Container it replaces (ADR-0025).
_Avoid_: Agent, Sidecar, Watchtower
