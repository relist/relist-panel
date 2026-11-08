# Basic auth groups through Traefik's file provider

A Domain (ADR-0013) had no password option. An operator protected a host by writing a `basicauth` middleware into the Compose labels by hand. That needs an Admin (ADR-0032), an escaped bcrypt hash, a copy in every Compose and a redeploy for every password change.

**Decision:** Admins manage host-wide **Basic auth groups** on their own page. A group holds usernames and passwords. A Domain picks one group or none. The panel writes one file per group into the file-provider directory Certificates already use (ADR-0022), `<data dir>/.traefik/dynamic/auth-<id>.yml`, as a `basicAuth` middleware `relist-auth-<id>` holding bcrypt hashes. A Domain with a group puts `relist-auth-<id>@file` first in its router's middleware chain. `panel.db` is the source of truth, and the directory is reconciled on startup and after every change. Passwords are stored in the clear next to their hash so an Admin can reveal them again. Each reveal is audited.

## Considered options

**Credentials on each Domain, as Coolify, CapRover and Dokploy do.** Rejected: the operator shares one set of credentials across several Domains, and changing it would mean editing and redeploying each one. Groups are what Nginx Proxy Manager calls Access Lists.

**Middleware in container labels instead of the file provider.** Rejected: hashes would show in `docker inspect`, need `$$` escaping through Compose interpolation, and apply only on redeploy.

**Hash only, no stored password.** Rejected by the operator: they often have to send a password to a client or teammate again. It is the same exposure as SSH keys (ADR-0020) and Certificate keys (ADR-0022).

**Several groups per Domain.** Rejected: every chained `basicAuth` must accept the single `Authorization` header the browser sends, so the result would be an intersection, not the union a user would expect.

**Groups per Project, managed by Project Editors.** Deferred: one host-wide list is enough for now, and Editors only pick a group.

## Consequences

- **Group edits apply immediately** to every Domain using the group. Picking a Domain's group applies on deploy, like any other Domain change.
- **A group cannot be deleted while a Domain uses it, or while a running container still routes through it.** Otherwise Traefik would not find the middleware and the router would break. The second check covers a Domain switched away from the group but not redeployed, and it reads the running containers' labels.
- **An empty group refuses everyone.** It is written with one random, unstored user rather than left open.
- **An install without the file-provider flags ignores groups.** A Domain naming a missing middleware breaks, which is the same guard as for Certificates: the README's upgrade section.
- **A Migration (ADR-0018) needs no change.** `panel.db` travels whole, and startup reconciliation rewrites the files.
- Spec: `.scratch/basic-auth/spec.md`.
