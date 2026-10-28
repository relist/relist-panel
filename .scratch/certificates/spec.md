# Spec — Uploaded TLS certificates, served by Traefik to Domains on provider None

Date: 2026-10-28.
Status: open — branch feat/certificates

## Problem

A Domain with HTTPS can get its certificate only from Let's Encrypt (HTTP-01) or from a
certificate resolver the operator configured in Traefik's static config by hand. An
operator who bought a certificate, or holds a wildcard certificate issued elsewhere,
has no way to give it to Traefik from the panel. Provider **None** today means "Traefik's
default self-signed certificate", which is useless on its own.

New HTTPS Domains also start on Let's Encrypt, which fails on any host whose DNS does
not point here or whose port 80 is closed.

## Goal and success criteria

- A new **Certificates** menu, host-wide, lists every uploaded Certificate with its name,
  the DNS names it covers (SANs), its issuer and its expiry. Expiry is red when under
  30 days away, and an expired Certificate shows an "Expired" tag.
- The operator adds a Certificate (name, certificate PEM, private key PEM), replaces the
  certificate and key of an existing one in place, and deletes one.
- A change applies at once, without a redeploy: Traefik's file provider picks it up.
- A Domain with HTTPS and provider **None** is served whichever uploaded Certificate
  covers its host, chosen by Traefik through SNI, wildcards included. With none
  covering it, Traefik serves its default self-signed certificate, as today.
- The default Certificate Provider of a new HTTPS Domain is **None**, not Let's Encrypt.
  Switching an existing HTTP Domain to HTTPS also starts from None.
- The private key is never returned by the API.

## Out of scope

- Choosing a Certificate per Domain. Traefik v3 cannot pin a certificate to a router;
  it picks from its store by SNI, so a picker would be cosmetic.
- Telling the operator whether a Certificate covers a Domain's host, on either the
  Domain form, the Domains table or the Certificates page. The operator reads the SANs.
- Generating certificates, CSRs or self-signed certificates in the panel.
- Encrypted (passphrase-protected) private keys.
- Using an uploaded Certificate for the panel's own host. The panel router keeps
  `tls.certresolver=letsencrypt`; whether Traefik prefers a stored certificate there is
  Traefik's behaviour and is not promised.
- Warnings about two Certificates covering the same host. Traefik then picks either,
  unpredictably; Replace exists so renewals never need two at once.

## Domain changes

- `CONTEXT.md` gains:

  **Certificate**:
  A TLS certificate and its private key, uploaded by the operator to the host-wide
  Certificates page and handed to Traefik's file provider. Traefik serves it to any HTTPS
  Domain on provider None whose host it covers, chosen by SNI; the panel does not link a
  Certificate to a Domain (ADR-0022).
  _Avoid_: SSL, Cert (as the entity name), Certificate resolver (that is how Traefik
  obtains certificates, e.g. Let's Encrypt).

- ADR-0022 records the decision (written alongside this spec).

## Data model

New migration `internal/migrations/00023_certificates.sql` (never edit a committed one):

```sql
CREATE TABLE certificates (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT    NOT NULL UNIQUE,
    certificate_pem TEXT    NOT NULL,  -- leaf first, then the chain, as uploaded (normalised)
    private_key_pem TEXT    NOT NULL,
    sans            TEXT    NOT NULL,  -- JSON array of DNS names from the leaf
    issuer          TEXT    NOT NULL,  -- leaf issuer CommonName, or the full DN when it has none
    not_after       INTEGER NOT NULL,  -- leaf expiry, Unix seconds UTC
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
```

`sans`, `issuer` and `not_after` are derived on every write, never edited directly. The
`domains` table does not change: there is no link from a Domain to a Certificate.

## Validation (`internal/certificates`)

On create and replace:

- **name** (create only): trimmed, 1–64 characters of letters, digits, `.`, `_`, `-`;
  unique (case-sensitive), else `409` on `name`.
- **certificate**: one or more `CERTIFICATE` PEM blocks, at most 64 KiB. The first block
  is the leaf. Text outside PEM blocks is dropped. Refused if it has no block, a block does
  not parse, or the leaf's `NotAfter` is already past.
- **privateKey**: one PKCS#1, PKCS#8 or EC private key PEM block, at most 16 KiB.
  Refused if encrypted (`Proc-Type: 4,ENCRYPTED` or `ENCRYPTED PRIVATE KEY`).
- The pair must load with `tls.X509KeyPair`; a mismatch is refused on `privateKey`
  ("private key does not match the certificate").
- The leaf must carry at least one DNS SAN, else refused on `certificate`. Traefik
  matches by SAN; a certificate without one would never be served.

Errors use the existing `{ "error": ..., "field": ... }` shape with fields `name`,
`certificate`, `privateKey`.

## API (`internal/certificates`, registered in `internal/httpapi/httpapi.go` like `sshkeys`)

| Method | Path | Body | Response |
| --- | --- | --- | --- |
| `GET` | `/api/certificates` | — | `200` list, ordered by name |
| `POST` | `/api/certificates` | `{ name, certificate, privateKey }` | `201` the Certificate |
| `PUT` | `/api/certificates/{id}` | `{ certificate, privateKey }` | `200` the Certificate |
| `DELETE` | `/api/certificates/{id}` | — | `204` |

A Certificate on the wire: `{ id, name, sans: string[], issuer, notAfter, createdAt,
updatedAt }`. No PEM is ever returned. Unknown id is `404`.

The Migration lock's middleware already refuses every non-allowlisted route while
locked, so these routes need nothing extra.

## Handing Certificates to Traefik

### Install file

`docker-compose.yml`, `traefik.command` gains:

```
- --providers.file.directory=/letsencrypt/dynamic
- --providers.file.watch=true
```

It reuses the existing `<data dir>/.traefik:/letsencrypt` mount, so no new volume. A
comment points at ADR-0022.

### Dynamic files

The panel writes one file per Certificate at `<data dir>/.traefik/dynamic/cert-<id>.yml`:

```yaml
# Written by relist-panel from the certificates table. Edits are overwritten.
tls:
  certificates:
    - certFile: |
        -----BEGIN CERTIFICATE-----
        ...
      keyFile: |
        -----BEGIN PRIVATE KEY-----
        ...
```

Traefik accepts the PEM content itself in `certFile`/`keyFile`, so the paths the panel
and Traefik see do not matter. The implementer confirms this against Traefik v3.7's
`tls` file-provider reference before relying on it; if inline content were refused,
the fallback is `cert-<id>.crt`/`.key` beside the YAML, referenced by Traefik's
container path `/letsencrypt/dynamic/...`.

Files are written to a temporary name in the same directory, `0600`, then renamed, so
the watcher never reads half a file. The directory is `0700`.

### Reconcile

`Reconcile(ctx)` makes the directory match the database: it writes every Certificate's
file and removes every `cert-*.yml` whose id is not in the table. Other files in the
directory are left alone, so an operator's own dynamic config survives. It runs:

- on panel startup, after migrations (covers a rebuild, a restored or migrated
  `panel.db`, and a lost directory);
- after every create, replace and delete, inside the same request.

A database write that succeeds while the file write fails returns `500` with the error;
the next Reconcile (any later write or restart) repairs the directory. Startup logs a
Reconcile failure and continues — the panel must still come up.

## UI (antd 6.5.1)

Before writing UI, read Ant Design's `llms.txt` and the component pages used (Table,
Modal, Form, Input.TextArea, Popconfirm, Tag), per AGENTS.md, and follow
`docs/GUIDELINES.md`.

### Certificates page

- Sidebar entry **Certificates** after **SSH keys**, icon `SafetyCertificateOutlined`,
  route `/certificates`, wired in `AppShell.tsx` and `routes.tsx` like SSH keys.
- Built after `SshKeysPage.tsx`; non-UI logic in `pages/certificates-view.ts` with tests
  (expiry state, SAN display).
- Intro line: "Traefik serves an uploaded certificate to any HTTPS domain set to
  provider None whose host it covers. Changes apply immediately."
- Table columns: **Name**, **Domains** (SANs, as tags), **Issuer**, **Expires** (date;
  red when under 30 days; an "Expired" tag when past). Row actions: **Replace**,
  **Delete** (Popconfirm: "Delete certificate <name>? Domains it covers fall back to
  Traefik's default certificate.").
- Empty state with an **Add certificate** button; the button also sits in the page header.

### Add / Replace modal

- **Add certificate**: Certificate Name (placeholder `My Certificate`), Certificate Data
  (TextArea, monospace, placeholder `-----BEGIN CERTIFICATE-----…`), Private Key
  (TextArea, monospace, placeholder `-----BEGIN PRIVATE KEY-----…`). OK button "Create".
- **Replace certificate**: the same modal; the name is shown read-only, both text areas
  start empty and are required. OK button "Replace".
- A server error naming a field lands on that field; any other error shows above the form.

### Domain form and table

- `domains-view.ts`: `domainFormValues()` defaults `certProvider` to `'none'` for a new
  Domain, and for an existing HTTP Domain (update the comment there).
- `DomainFormModal.tsx`: the None help text becomes "Traefik serves an uploaded
  certificate (Certificates page) that covers this host, otherwise its default
  self-signed certificate."
- `httpsTag`: "TLS (default cert)" becomes "TLS".
- Existing Domains keep their stored provider. The backend already normalises an empty
  provider to `none`, so no Go change in `internal/domains`.

## Docs

- `README.md`: a Certificates section, and the "Upgrading an existing install" section
  says the install file must be updated for uploaded certificates to be served.
- `.scratch/domains/spec.md`: a dated note that the default provider is now None (this
  spec).
- `CONTEXT.md`: the **Certificate** term above.

## Testing

Go:

- Validation: valid leaf+chain; mismatched key; malformed PEM; no PEM block; encrypted
  key (both forms); expired leaf; no DNS SAN; oversize input; duplicate name (`409`).
  Test certificates are generated in the test with `crypto/x509`, never checked in.
- Store: create, replace (derived fields refresh, `updated_at` moves), delete, list order.
- Reconcile: writes one file per row with the expected YAML; removes stale `cert-*.yml`;
  leaves other files alone; files are `0600`; no temporary files left behind.
- HTTP: each route's status codes, the private key absent from every response, field
  errors.

Frontend:

- `certificates-view.ts`: expiry state at 31 days, 29 days and past.
- Certificates page: list renders, add submits, a field error lands on the field,
  replace sends no name.
- `domains-view.test.ts` / `DomainFormModal.test.tsx`: new and HTTP-to-HTTPS defaults are
  `none`; `httpsTag` reads "TLS".

Manual: on the live install after updating the install file, upload a certificate for a
host, set its Domain to HTTPS + None, and check the served certificate with
`openssl s_client -servername <host> -connect <host>:443`.

## Resolved questions

- **None means SNI selection, not a per-Domain pick** (Dokploy's behaviour); Traefik
  cannot pin a certificate to a router.
- **No coverage hints** anywhere; the operator reads the SANs.
- **Replace in place** rather than add-then-delete, so renewals never have two
  Certificates covering one host.
- **List shows** name, SANs, issuer, expiry with a 30-day warning.
