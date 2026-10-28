# Uploaded certificates through Traefik's file provider

ADR-0013 gave HTTPS Domains a certificate from Let's Encrypt, or from a resolver the operator configured in Traefik's static config by hand. An operator holding a bought or wildcard certificate could not hand it to Traefik from the panel, and provider None served only Traefik's self-signed default.

**Decision:** the operator uploads **Certificates** (certificate chain and private key, PEM) on a host-wide Certificates page. The panel keeps them in `panel.db` and writes one file per Certificate into Traefik's file-provider directory, `<data dir>/.traefik/dynamic/cert-<id>.yml`, with the PEM inline. Traefik serves a Certificate to any router whose host it covers, chosen by SNI. A Domain uses one by being HTTPS with provider None: the panel never links a Certificate to a Domain. New HTTPS Domains now default to None instead of Let's Encrypt.

The install `docker-compose.yml` gains `--providers.file.directory=/letsencrypt/dynamic` and `--providers.file.watch=true`, reusing the existing `.traefik` mount. The database is the source of truth: the directory is reconciled on startup and after every change, and `cert-*.yml` files with no row are removed.

## Considered options

**A Certificate chosen per Domain.** Rejected: Traefik v3 cannot pin a certificate to a router. It picks from its store by SNI, so a picker would only be a label, and a wrong pick would silently do nothing.

**Telling the operator which Certificate covers a host.** Rejected for now by the operator: the list shows each Certificate's SANs, and that is enough.

**Add and delete only, as Dokploy does.** Rejected: renewing would mean two Certificates covering one host for a while, and Traefik picks between them unpredictably. Replace swaps the pair in place.

**Files with paths instead of inline PEM.** Not needed: the panel sees `<data dir>/.traefik` while Traefik sees `/letsencrypt`, and inline content sidesteps the mismatch.

## Consequences

- **Certificate changes apply immediately**, unlike Domains (ADR-0013), because the file provider watches the directory.
- **The panel cannot see Traefik's static config.** An install upgraded without the file-provider flags ignores every Certificate, and nothing says so. The README's "Upgrading an existing install" section is the guard, as for ADR-0013.
- **A new HTTPS Domain with no covering Certificate serves Traefik's self-signed certificate** until the operator uploads one or chooses Let's Encrypt. Existing Domains keep their provider.
- **The private key sits in `panel.db` and on disk at `0600`**, and is never returned by the API. Like SSH keys (ADR-0020), this is not a security boundary: the single admin owns the host.
- **A Migration (ADR-0018) needs no change.** `panel.db` travels whole, and startup reconciliation rewrites the directory on the new host.
- Spec: `.scratch/certificates/spec.md`.
