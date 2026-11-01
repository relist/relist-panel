# 03 — Update check, settings and status API

Status: closed
Blocked by: 01

Spec: [../spec.md](../spec.md) — "Update check", "API".

## Do

- Add schema migration `00025_panel_updates.sql` with the tables `panel_update_settings`
  and `panel_updates`. Write a new file; never edit an existing migration.
- Add `internal/panelupdate`:
  - `Manifest` parse and verify (ed25519, standard library only);
  - `key.go` with a placeholder public key constant, which ticket 02 replaces. Tests
    sign with a test key and inject it; they never use the constant;
  - `Checker`, which fetches the latest release's two files with a 30 s timeout and a
    10 MiB cap;
  - semver compare against `main.version`;
  - the daily schedule, at startup plus 5 minutes.
- The version is `dev` when the build was not stamped. Checking still runs, and the
  status says *Development build: updates are not offered*.
- Add the routes `GET /api/panel-update/status`, `POST …/check` and
  `PUT …/settings`, admin-only.
- Tests:
  - a bad signature drops the manifest;
  - an older or equal version is not offered;
  - a failed check keeps the last good result;
  - with the check off, nothing goes out on the daily schedule.
