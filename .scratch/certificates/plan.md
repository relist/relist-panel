# Uploaded Certificates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A host-wide Certificates page whose uploads Traefik serves by SNI to HTTPS Domains on provider None, with None as the new default provider.

**Architecture:** New Go package `internal/certificates` (parse/validate, SQLite store, file writer, HTTP service), modelled on `internal/sshkeys`. The database is the source of truth; `Reconcile` rewrites `<data dir>/.traefik/dynamic/cert-<id>.yml` on startup and after every write, and Traefik's file provider (new flags in the install `docker-compose.yml`) watches that directory. Frontend adds a Certificates page modelled on `SshKeysPage.tsx` and flips the Domain form's default provider.

**Tech Stack:** Go (`crypto/tls`, `crypto/x509`, `encoding/pem`, `gopkg.in/yaml.v3`, goose SQL migrations), React + antd 6.5.1 + SWR + vitest.

**Spec:** `.scratch/certificates/spec.md` · ADR: `docs/adr/0022-uploaded-certificates.md`

## Global Constraints

- Never edit a committed migration; the new one is `internal/migrations/00023_certificates.sql`.
- The private key is never returned by any API response.
- Name: trimmed, 1–64 characters of `A-Za-z0-9._-`, unique, case-sensitive; a duplicate is `409` on `name`.
- Size limits: certificate ≤ 64 KiB, private key ≤ 16 KiB.
- Error body shape: `{"error": "...", "field": "name" | "certificate" | "privateKey"}` (field omitted when none).
- Dynamic files: `<data dir>/.traefik/dynamic/cert-<id>.yml`, mode `0600`, directory `0700`, temp file + rename.
- Reconcile removes only `cert-*.yml` files with no row; every other file is left alone.
- Commit messages: Conventional Commits, no scope, no `Co-Authored-By` / session trailers (`docs/agents/commit-conventions.md`).
- Before any UI work: read `https://ant.design/llms.txt` and the Table, Modal, Form, Input (TextArea), Popconfirm, Tag pages; follow `docs/GUIDELINES.md`.
- UI copy is fixed by the spec (intro line, None help text, Popconfirm text, button labels) — copy it verbatim.

## Review Focus

1. **Pasted PEM with CRLF line endings or surrounding whitespace** (copied from Windows or a CA email) — must be accepted. Test in Task 1.
2. **Chain pasted CA-first** — the first block is not the leaf, so the key does not match; the error on `privateKey` must say to put the server certificate first. Test in Task 1.
3. **`BEGIN EC PRIVATE KEY` and `BEGIN RSA PRIVATE KEY` keys** (not just PKCS#8) — must be accepted. Test in Task 1.
4. **First start on an install with no `.traefik/dynamic` directory, or a cert file already deleted by hand** — Reconcile creates the directory and does not fail. Test in Task 3.
5. **Replace whose file write fails** — the response is `500`, and the next Reconcile writes the new pair (the DB already holds it). Test in Task 4.

---

### Task 1: Parse and validate a certificate/key pair

**Files:**
- Create: `internal/certificates/parse.go`
- Test: `internal/certificates/parse_test.go`

**Interfaces:**
- Produces:
  ```go
  type FieldError struct{ Field, Message string } // Error() string { return Field + ": " + Message }
  type Parsed struct {
      CertificatePEM string    // normalised: only the CERTIFICATE blocks, re-encoded, leaf first
      PrivateKeyPEM  string    // normalised: the one key block, re-encoded
      SANs           []string  // leaf DNSNames, in certificate order
      Issuer         string    // leaf Issuer.CommonName, else Issuer.String()
      NotAfter       time.Time // leaf NotAfter, UTC
  }
  func Parse(certPEM, keyPEM string, now time.Time) (Parsed, *FieldError)
  ```

- [ ] **Step 1: Write the failing tests.** Add a test helper `newPair(t, opts)` that generates a self-signed leaf with `crypto/x509` (options: DNS SANs, NotAfter, key type RSA/ECDSA/Ed25519-PKCS8, optional CA cert to sign it) and returns cert PEM, key PEM. Tests, all in `parse_test.go`:
  - `TestParseValidLeafAndChain`: leaf + CA PEM concatenated → `SANs == []string{"example.com","*.example.com"}`, `NotAfter` equals the leaf's, `Issuer` is the CA's CommonName, `CertificatePEM` contains 2 blocks.
  - `TestParseAcceptsCRLFAndWhitespace`: the same PEMs with `\n` → `\r\n` and leading/trailing blank lines and text outside blocks → no error.
  - `TestParseKeyFormats`: PKCS#8 (`PRIVATE KEY`), PKCS#1 (`RSA PRIVATE KEY`), SEC1 (`EC PRIVATE KEY`) → no error each.
  - `TestParseRefuses` (table): each case asserts `err.Field` and that `err.Message` contains the given text:
    | case | field | message contains |
    | --- | --- | --- |
    | empty certificate | certificate | `no CERTIFICATE block` |
    | garbage inside a CERTIFICATE block | certificate | `does not parse` |
    | leaf NotAfter = now − 1h | certificate | `expired` |
    | leaf with no DNS SAN (CN only) | certificate | `no DNS name` |
    | certificate > 64 KiB | certificate | `64 KiB` |
    | empty key | privateKey | `no private key block` |
    | `ENCRYPTED PRIVATE KEY` block | privateKey | `encrypted` |
    | `RSA PRIVATE KEY` with `Proc-Type: 4,ENCRYPTED` header | privateKey | `encrypted` |
    | key from another pair | privateKey | `does not match` |
    | chain pasted CA first | privateKey | `server certificate first` |
    | key > 16 KiB | privateKey | `16 KiB` |

- [ ] **Step 2: Run** `go test ./internal/certificates/ -run TestParse -v` — Expected: FAIL (package does not compile: `Parse` undefined).

- [ ] **Step 3: Implement `Parse` in `parse.go`.** Size checks first. Walk `pem.Decode` over the cert input collecting `CERTIFICATE` blocks; `x509.ParseCertificate` each. Key: first block whose type ends in `PRIVATE KEY`; refuse when its type is `ENCRYPTED PRIVATE KEY` or `x509.IsEncryptedPEMBlock`-style `Proc-Type` header contains `ENCRYPTED`. Check the pair with `tls.X509KeyPair(normalisedCert, normalisedKey)`; on failure, if any non-first certificate's public key matches the private key, return the "does not match … put the server certificate first" message variant ("private key does not match the first certificate; put the server certificate first, then the chain"), else "private key does not match the certificate". Then expiry (`!now.Before(leaf.NotAfter)`) and `len(leaf.DNSNames) == 0`.

- [ ] **Step 4: Run** `go test ./internal/certificates/ -run TestParse -v` — Expected: PASS.

- [ ] **Step 5: Commit** — `git add internal/certificates && git commit -m "feat: parse and validate uploaded certificate pairs"`

### Task 2: Migration and store

**Files:**
- Create: `internal/migrations/00023_certificates.sql`
- Create: `internal/certificates/store.go`
- Test: `internal/certificates/store_test.go`

**Interfaces:**
- Consumes: `Parsed`, `FieldError` (Task 1).
- Produces:
  ```go
  type Certificate struct {
      ID int64; Name string
      CertificatePEM, PrivateKeyPEM string
      SANs []string; Issuer string; NotAfter time.Time
      CreatedAt, UpdatedAt time.Time
  }
  var ErrNameTaken = errors.New("A certificate with this name already exists")
  func NewStore(db *sql.DB) *Store
  func CleanName(name string) (string, *FieldError)
  func (s *Store) List(ctx context.Context) ([]Certificate, error)            // ordered by name
  func (s *Store) Create(ctx context.Context, name string, p Parsed) (Certificate, error)
  func (s *Store) Replace(ctx context.Context, id int64, p Parsed) (Certificate, error) // sql.ErrNoRows when missing
  func (s *Store) Delete(ctx context.Context, id int64) error                  // sql.ErrNoRows when missing
  ```

- [ ] **Step 1: Write the migration** exactly as the spec's Data model section (goose `-- +goose Up` / `-- +goose Down` like `00021_ssh_keys.sql`; `name` is plain `UNIQUE`, not `COLLATE NOCASE`; `sans` is a JSON array; times are Unix seconds).

- [ ] **Step 2: Write the failing tests** in `store_test.go`, opening the DB the same way `internal/sshkeys/store_test.go` does:
  - `TestStoreCreateListOrder`: create `b`, `a` → `List` returns `a`, `b`; SANs round-trip as a slice.
  - `TestStoreDuplicateName`: second `Create("a")` → `errors.Is(err, ErrNameTaken)`; `Create("A")` succeeds (case-sensitive).
  - `TestStoreReplace`: replace with a pair with different SANs/NotAfter → returned and listed values change, `Name` and `CreatedAt` unchanged, `UpdatedAt` moves forward; unknown id → `sql.ErrNoRows`.
  - `TestStoreDelete`: delete → gone from `List`; unknown id → `sql.ErrNoRows`.
  - `TestCleanName`: `"  my-cert.1 "` → `"my-cert.1"`; `""`, 65 chars, `"my cert"`, `"cert/1"` → `FieldError{Field: "name"}`.

- [ ] **Step 3: Run** `go test ./internal/certificates/ -run 'TestStore|TestCleanName' -v` — Expected: FAIL (undefined).

- [ ] **Step 4: Implement `store.go`.** Name pattern `^[A-Za-z0-9._-]{1,64}$` after `TrimSpace`. Map a SQLite UNIQUE violation to `ErrNameTaken` the way `internal/sshkeys/store.go` does. Inject the clock as a `now func() time.Time` field defaulting to `time.Now` so tests can advance it.

- [ ] **Step 5: Run** the tests from Step 3, then `go test ./internal/migrations/... ./internal/database/...` — Expected: PASS (migration checksums/registration tests stay green).

- [ ] **Step 6: Commit** — `git add internal/migrations/00023_certificates.sql internal/certificates && git commit -m "feat: store uploaded certificates"`

### Task 3: Write Traefik's dynamic files (Reconcile)

**Files:**
- Create: `internal/certificates/traefik.go`
- Test: `internal/certificates/traefik_test.go`

**Interfaces:**
- Consumes: `Certificate`, `Store.List` (Task 2).
- Produces:
  ```go
  func DynamicDir(dataDir string) string // filepath.Join(dataDir, ".traefik", "dynamic")
  func WriteDir(dir string, certs []Certificate) error
  func Reconcile(ctx context.Context, store *Store, dataDir string) error // List + WriteDir(DynamicDir(dataDir), ...)
  ```

- [ ] **Step 1: Confirm inline PEM.** Read Traefik v3.7's file-provider TLS reference (`https://doc.traefik.io/traefik/https/tls/`, "User defined" certificates). Expected: `certFile`/`keyFile` accept the PEM content itself. If not, stop and switch to `cert-<id>.crt`/`.key` files referenced as `/letsencrypt/dynamic/cert-<id>.crt` per the spec's fallback, and note it in the spec.

- [ ] **Step 2: Write the failing tests** in `traefik_test.go`, using `t.TempDir()`:
  - `TestWriteDirWritesOneFilePerCertificate`: two certs (ids 3, 7) → files `cert-3.yml`, `cert-7.yml`; unmarshal each with `yaml.v3` into `struct{TLS struct{Certificates []struct{CertFile, KeyFile string} } }` and assert the PEMs equal the inputs; first line is the `# Written by relist-panel from the certificates table. Edits are overwritten.` comment; file mode `0600`; dir mode `0700`.
  - `TestWriteDirRemovesStaleCertFilesOnly`: pre-create `cert-9.yml`, `my-middlewares.yml`, `cert-notes.txt` → after `WriteDir` with no certs only `cert-9.yml` is gone.
  - `TestWriteDirCreatesMissingDir`: dir does not exist → created, no error.
  - `TestWriteDirLeavesNoTempFiles`: after a write, the directory holds only the expected `cert-*.yml` files.
  - `TestWriteDirToleratesAlreadyDeletedFile`: a cert removed from the list whose file is already gone → no error.

- [ ] **Step 3: Run** `go test ./internal/certificates/ -run TestWriteDir -v` — Expected: FAIL (undefined).

- [ ] **Step 4: Implement `traefik.go`.** `os.MkdirAll(dir, 0o700)` then `os.Chmod(dir, 0o700)`. Marshal with `yaml.v3` (PEM as literal block strings), write via `os.CreateTemp(dir, ".cert-*.tmp")`, `Chmod 0600`, `Sync`, `Close`, `os.Rename`. Stale removal matches `^cert-\d+\.yml$` and ignores `os.ErrNotExist`.

- [ ] **Step 5: Run** the tests from Step 3 — Expected: PASS.

- [ ] **Step 6: Commit** — `git add internal/certificates && git commit -m "feat: write uploaded certificates into traefik's file provider directory"`

### Task 4: HTTP service, wiring, startup reconcile and install file

**Files:**
- Create: `internal/certificates/http.go`
- Test: `internal/certificates/http_test.go`
- Modify: `internal/httpapi/httpapi.go` (register next to `sshkeys.New(db).RegisterRoutes(mux)`, ~line 146)
- Modify: `main.go` (call Reconcile next to `cleanSourceStaging`, ~line 128)
- Modify: `docker-compose.yml` (traefik `command`)

**Interfaces:**
- Consumes: `Parse` (Task 1), `Store`, `CleanName`, `ErrNameTaken` (Task 2), `Reconcile` (Task 3).
- Produces:
  ```go
  func New(db *sql.DB, dataDir string) *Service
  func (s *Service) RegisterRoutes(mux *http.ServeMux)
  // GET/POST /api/certificates, PUT/DELETE /api/certificates/{id}
  // wire shape: {id, name, sans, issuer, notAfter, createdAt, updatedAt}
  ```

- [ ] **Step 1: Write the failing tests** in `http_test.go` (pattern: `internal/sshkeys/http_test.go`):
  - `TestCreateReturnsCertificateWithoutKey`: `POST` valid pair → `201`; body has `sans`, `issuer`, `notAfter`; raw body does not contain `PRIVATE KEY` nor `BEGIN CERTIFICATE`; `cert-<id>.yml` exists under `DynamicDir(dataDir)`.
  - `TestCreateFieldErrors` (table): bad name → `400` `field: "name"`; mismatched key → `400` `field: "privateKey"`; duplicate name → `409` `field: "name"`.
  - `TestListOmitsKeys`: `GET` → `200`, ordered by name, raw body has no `PRIVATE KEY`.
  - `TestReplace`: `PUT` new pair → `200`, SANs changed, file content now holds the new PEM; a `name` in the body is ignored; unknown id → `404`.
  - `TestDelete`: `DELETE` → `204`, file gone; unknown id → `404`.
  - `TestReplaceWhenFileWriteFails`: make `DynamicDir` a regular file so writes fail → `PUT` returns `500`; then restore it as a directory and call `Reconcile` → the file holds the new pair.

- [ ] **Step 2: Run** `go test ./internal/certificates/ -run 'TestCreate|TestList|TestReplace|TestDelete' -v` — Expected: FAIL.

- [ ] **Step 3: Implement `http.go`.** Request bodies: create `{name, certificate, privateKey}`, replace `{certificate, privateKey}`; cap the body with `http.MaxBytesReader` at 128 KiB. Order: `CleanName` (create) → `Parse(..., time.Now())` → store → `Reconcile`; a Reconcile error is `500` "certificate saved but Traefik's certificate files could not be written: <err>". `FieldError` → `400 {"error","field"}`; `ErrNameTaken` → `409` with `field: "name"`; `sql.ErrNoRows` → `404`.

- [ ] **Step 4: Wire it.** `httpapi.go`: `certificates.New(db, dataDir).RegisterRoutes(mux)`. `main.go`: after `cleanSourceStaging`, `if err := certificates.Reconcile(ctx, certificates.NewStore(db), cfg.DataDir); err != nil { log.Printf("reconcile certificates: %v", err) }` — never fatal. `docker-compose.yml`: after the `letsencrypt` resolver lines add
  ```yaml
      # Uploaded Certificates (ADR-0022): the panel writes one file per
      # certificate here from panel.db; Traefik serves them by SNI.
      - --providers.file.directory=/letsencrypt/dynamic
      - --providers.file.watch=true
  ```

- [ ] **Step 5: Run** `go test ./internal/certificates/... ./internal/httpapi/...` and `go vet ./...` — Expected: PASS, no vet output.

- [ ] **Step 6: Commit** — `git add internal/certificates internal/httpapi/httpapi.go main.go docker-compose.yml && git commit -m "feat: serve the certificates api and reconcile traefik files on startup"`

### Task 5: Certificates page

**Files:**
- Modify: `web/src/data/types.ts` (next to `SshKey`), `web/src/data/api.ts` (next to the SSH key calls, ~line 353)
- Create: `web/src/pages/certificates-view.ts`, `web/src/pages/certificates-view.test.ts`
- Create: `web/src/pages/CertificatesPage.tsx`, `web/src/pages/CertificatesPage.test.tsx`
- Create: `web/src/components/CertificateFormModal.tsx`
- Modify: `web/src/app/paths.ts`, `web/src/app/routes.tsx`, `web/src/app/AppShell.tsx` (mirror every `ssh-keys` occurrence)

**Interfaces:**
- Consumes: the wire shape from Task 4.
- Produces:
  ```ts
  export interface Certificate { id: number; name: string; sans: string[]; issuer: string; notAfter: string; createdAt: string; updatedAt: string }
  export type CertificateInput = { name: string; certificate: string; privateKey: string }
  api.listCertificates(): Promise<Certificate[]>
  api.createCertificate(input: CertificateInput): Promise<Certificate>
  api.replaceCertificate(id: number, input: Omit<CertificateInput, 'name'>): Promise<Certificate>
  api.deleteCertificate(id: number): Promise<void>
  export function expiryState(notAfter: string, now: Date): 'ok' | 'soon' | 'expired' // soon = < 30 days
  paths.certificates = '/certificates'
  ```

- [ ] **Step 1: Read the antd docs** listed in Global Constraints and `docs/GUIDELINES.md`; check every prop used exists in antd 6.5.1 and is not deprecated.

- [ ] **Step 2: Write the failing tests.**
  - `certificates-view.test.ts`: with `now = 2026-10-28T00:00:00Z`: notAfter +31 days → `'ok'`; +29 days → `'soon'`; −1 second → `'expired'`.
  - `CertificatesPage.test.tsx` (mock `api` like `SshKeysPage.test.tsx`):
    - renders rows with name, each SAN as a tag, issuer, and an "Expired" tag for an expired row;
    - shows the intro line verbatim: "Traefik serves an uploaded certificate to any HTTPS domain set to provider None whose host it covers. Changes apply immediately.";
    - Add: fill name/certificate/key, click "Create" → `createCertificate` called with `{name, certificate, privateKey}`;
    - a rejected create with `ApiError` field `privateKey` shows its message under the Private Key field;
    - Replace: name shown read-only, click "Replace" → `replaceCertificate(id, {certificate, privateKey})`, no `name` key;
    - Delete: confirm Popconfirm "Delete certificate <name>? Domains it covers fall back to Traefik's default certificate." → `deleteCertificate(id)`.

- [ ] **Step 3: Run** `pnpm --dir web test -- certificates CertificatesPage` — Expected: FAIL (modules missing).

- [ ] **Step 4: Implement** types, api calls, `expiryState`, `CertificateFormModal` (props: `open`, `certificate?: Certificate` — present means Replace mode, `saving`, `error?: ApiError`, `onCancel`, `onSubmit(input)`; monospace `Input.TextArea` with `autoSize={{ minRows: 5, maxRows: 12 }}`; placeholders `My Certificate`, `-----BEGIN CERTIFICATE-----…`, `-----BEGIN PRIVATE KEY-----…`; field-error mapping like `DomainFormModal.tsx`), `CertificatesPage` (SWR key `/api/certificates`, header button and empty state "Add certificate", Expires column red text for `'soon'`, `Tag` "Expired" for `'expired'`), and the sidebar entry "Certificates" with `SafetyCertificateOutlined` after "SSH keys".

- [ ] **Step 5: Run** `pnpm --dir web test` and `pnpm --dir web typecheck` — Expected: PASS.

- [ ] **Step 6: Commit** — `git add web/src && git commit -m "feat: add the certificates page"`

### Task 6: Provider None by default in the Domain form

**Files:**
- Modify: `web/src/pages/domains-view.ts:24,39-42,108-120`
- Modify: `web/src/components/DomainFormModal.tsx:149-152`
- Test: `web/src/pages/domains-view.test.ts`, `web/src/components/DomainFormModal.test.tsx`

- [ ] **Step 1: Update the tests first.** `domainFormValues()` → `certProvider: 'none'`; `domainFormValues(httpDomain)` → `certProvider: 'none'`; `httpsTag({https: true, certProvider: 'none'})` → `'TLS'`; modal with HTTPS on and provider None shows "Traefik serves an uploaded certificate (Certificates page) that covers this host, otherwise its default self-signed certificate."; any existing assertion of `'letsencrypt'` as the default or of "TLS (default cert)" is changed, not deleted.

- [ ] **Step 2: Run** `pnpm --dir web test -- domains-view DomainFormModal` — Expected: FAIL on the changed assertions.

- [ ] **Step 3: Implement** the three edits; rewrite the comment at `domains-view.ts:39-40` to say an HTTP domain switched to HTTPS starts from None, the default.

- [ ] **Step 4: Run** `pnpm --dir web test` and `pnpm --dir web typecheck` — Expected: PASS.

- [ ] **Step 5: Commit** — `git add web/src && git commit -m "feat: default new https domains to certificate provider none"`

### Task 7: Docs and embedded frontend

**Files:**
- Modify: `CONTEXT.md` (add **Certificate** after **Domain**, text verbatim from the spec's Domain changes)
- Modify: `README.md` (Certificates section after the HTTPS/domains section; "Upgrading an existing install" gains the file-provider flags)
- Modify: `.scratch/domains/spec.md` (dated note under the Certificate Provider line: "2026-10-28: the default is now None — see `.scratch/certificates/spec.md`.")
- Regenerate: embedded frontend (`task build-web` / `go generate ./internal/webassets/...`)

- [ ] **Step 1: Edit the three docs.** README Certificates section says: upload on the Certificates page; a Domain uses one by HTTPS + provider None; Traefik picks by SNI, wildcards included; changes apply immediately; check with `openssl s_client -servername <host> -connect <host>:443`. Upgrading section: an install file without `--providers.file.directory` ignores uploaded certificates silently.

- [ ] **Step 2: Rebuild the embedded frontend** with `task build-web`; then run `task test` — Expected: PASS.

- [ ] **Step 3: Commit** — `git add CONTEXT.md README.md .scratch/domains/spec.md && git commit -m "docs: describe uploaded certificates and the none default"`, then `git add -A internal/webassets && git commit -m "chore: rebuild the embedded frontend"`

- [ ] **Step 4: Manual check on the live install** (operator): update the install `docker-compose.yml`, recreate Traefik and the panel, upload a certificate, set a Domain to HTTPS + None, and verify the served certificate with `openssl s_client`.
