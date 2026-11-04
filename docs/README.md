# relist-panel docs, by system

Start here to learn how one part of the panel works. Each row is one system: what it
does in a line, and where to read more. A system with a guide page links to it. Until
one is written, the row links to the decisions (ADRs) that shaped it.

Terms in **bold** across these docs are defined in [CONTEXT.md](../CONTEXT.md). The
operator's view (installing, `.env`, updating) is in the top-level
[README.md](../README.md).

| System | What it does | Read |
|---|---|---|
| Traefik | Routes every Domain and the panel. Its static config is generated from a base and the operator's Traefik override and applied with self-heal. It keeps the Access log and serves Let's Encrypt and uploaded Certificates | [guide/traefik.md](guide/traefik.md), ADR [0030](adr/0030-panel-managed-traefik-static-config.md) |
| Install and Panel update | The install script, the Install file, and moving an Install to a newer panel version with a health check and rollback | ADRs [0024](adr/0024-panel-self-update.md), [0025](adr/0025-one-updater-and-install-script.md), [0026](adr/0026-release-candidates.md) |
| Migration | Moving the whole panel, its data and its Composes to another host | ADRs [0018](adr/0018-server-migration.md), [0019](adr/0019-migration-driven-from-the-terminal.md) |
| Deployments and Sources | Pulling a Compose from Git, queueing deploys, and rolling Services out without downtime | ADRs [0016](adr/0016-sources-and-deployment-queue.md), [0017](adr/0017-deployments-freeze-inputs-when-queued.md), [0023](adr/0023-zero-downtime-rollout.md) |
| Domains and networking | The hosts a Service answers on, the Traefik labels injected on deploy, and `relist-network` | ADRs [0013](adr/0013-panel-managed-domains.md), [0014](adr/0014-relist-network-only-where-needed.md) |
| Variables | Environment, Compose and Variable set values, and how they are scoped | ADR [0015](adr/0015-scoped-variables.md) |
| Volumes | Browsing and editing files inside a Volume | ADRs [0008](adr/0008-volume-file-access.md), [0010](adr/0010-volume-file-access-is-one-run-per-operation.md) |
| Vulnerability scanning | Scanning the images a Compose runs | ADR [0021](adr/0021-vulnerability-scanning.md) |
| SSH keys | The key library used for Git Sources and Migration | ADR [0020](adr/0020-ssh-key-library.md) |
| Certificates | Uploaded certificates served by Traefik | ADR [0022](adr/0022-uploaded-certificates.md), and [guide/traefik.md](guide/traefik.md) |

Other docs: [GUIDELINES.md](GUIDELINES.md) is the UI contract, [adr/](adr/) holds every
decision in order, and [agents/](agents/) holds the instructions for AI coding agents.

## Adding a guide page

A change to a system updates its guide page. If the system has none yet, the change adds
one under `guide/` and points its row here at it. A guide page is written for someone new
to the repo and has these seven sections, in this order:

1. What it does
2. How it works
3. Where the code is
4. Where the data is
5. What can break and how to recover
6. How to test
7. Decisions

Keep the page about how the system works now. Why it is that way belongs in an ADR, and
section 7 links to it.
