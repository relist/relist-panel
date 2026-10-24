# 01 — Add `relist-panel create-admin`

Status: closed

Spec: [../spec.md](../spec.md) — section "The command" is the contract.

## Where

- `main.go`: dispatch `os.Args[1] == "create-admin"` next to `reset-2fa`:
  `os.Exit(runCreateAdmin(os.Args[2:], os.Stdin, os.Stdout, os.Stderr, isTerminal))`.
- `cli.go`: add `runCreateAdmin`, shaped like `runResetTwoFactor` (own
  `flag.FlagSet` with `ContinueOnError`, usage const, `config.Load()`,
  `database.OpenAndMigrate(cfg.DatabasePath())`, `defer db.Close()`).
  Usage const:
  `usage: relist-panel create-admin [--email ADDRESS] [--generate | --password-stdin]`.
- `internal/auth`: add an exported
  `CreateAdmin(ctx, db, email, password string, now func() time.Time) (string, error)`
  that runs `validateEmail`, `validatePassword`, bcrypt
  (`bcrypt.DefaultCost`) and `insertAdmin`, returning the normalized email.
  `ErrAdminAlreadyExists` passes through. Put it in `internal/auth/password.go`
  beside `SeedAdmin` (ticket 02 deletes `SeedAdmin`).
- `internal/auth`: add `GeneratePassword() (string, error)` — 18 bytes from
  `crypto/rand`, `base64.RawURLEncoding`.

## Terminal input

- Hidden input: `github.com/charmbracelet/x/term` `ReadPassword(fd)`; it is
  already in `go.mod` as indirect — run `go mod tidy` so it becomes direct.
  Print a newline after each hidden read (ReadPassword swallows the Enter).
- Terminal check: `term.IsTerminal(os.Stdin.Fd())`.
- Keep `runCreateAdmin` testable: take `stdin io.Reader`, `stdout`, `stderr`,
  and inject the terminal check and the hidden-read function (e.g. a small
  struct or two func params). Tests pass fakes; `main.go` passes the real
  ones. Read visible lines (email, `--password-stdin`) through one
  `bufio.Reader` on stdin.

## Tests (`cli_test.go` or a new `create_admin_test.go` in package main)

Use a temp `RELIST_DATA_DIR` (see how existing `reset-2fa` tests set up the
config/DB, and follow them).

- `--email a@example.com --password-stdin` with stdin `"long enough pw\n"` →
  exit 0, stdout `Created admin a@example.com.`, row exists, bcrypt matches,
  `password_from_env = 0`.
- `--email A@Example.com --generate` → exit 0; stdout has a `Password: ` line
  with 24 characters; signing in with it would match (bcrypt compare on the
  row); email stored lowercased.
- Second run when an admin exists → exit 1, stderr
  `create-admin: an admin account already exists`, row unchanged.
- `--generate --password-stdin` → exit 2.
- Non-terminal, no password flag → exit 2 with the spec's message.
- `--password-stdin` without `--email` → exit 2.
- Interactive (fake terminal): email line, password, matching confirm → 0;
  mismatched confirm → 1 with `passwords do not match`; empty password →
  generated and printed.
- Short password → exit 1 with `create-admin: password must be at least 8 characters`.

## Done when

- `go test ./...` passes; `go vet ./...` clean.
- Nothing else changes in this ticket: env seeding and web setup still work.
- One commit, Conventional Commits, no scope, no trailers, e.g.
  `feat: create the admin from a terminal with relist-panel create-admin, prompting for the password or generating one`.

## Comments

Implemented on `feat/cli-admin` in `2aa020f`. `runCreateAdmin` in `cli.go`
takes a `console` (`isTerminal`, `readPassword`), not a type named
`terminal`, because `main.go` already imports `internal/terminal`. It checks
`auth.AdminExists` before prompting, so an install that already has an admin
fails before anyone types a password; `auth.CreateAdmin` still returns
`ErrAdminAlreadyExists` if another create gets in first. `GeneratePassword`
lives in `auth.go` beside `newSessionToken`. Goose's migration log lines go to
stderr on a fresh database, the same as `reset-2fa`; stdout holds only the
result. Verified with `go test -race . ./internal/auth`, `go test ./...`,
`go vet ./...`, and the built binary: `--generate` (stdout only), a second
run (exit 1), no terminal (exit 2), and interactive under a pty via
`script` (password not echoed).
