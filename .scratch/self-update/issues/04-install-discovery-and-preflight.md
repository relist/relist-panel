# 04 — Install discovery and Preflight

Status: closed
Blocked by: 01, 03

Spec: [../spec.md](../spec.md) — "The Install, found from labels", "Preflight",
"Install invariant kinds".

## Do

- Discovery: inspect the panel's own Container and read the three Compose labels. Return
  `Install{Project, Dir, ConfigFiles}` or an `Unavailable` reason.
- File read: use a one-run helper Container of the current image, with the Install
  directory mounted `:ro` at the same path. The panel mounts nothing new (ADR-0010
  pattern).
- Preflight, steps 1–7 of the spec. Each step is its own function, and the report is a
  plain struct.
  - The three-way merge is `git merge-file -p`. Exit 0 means merged, 1–127 is the
    number of conflicts, and 255 is an error. Exit codes are never negative.
  - The scratch directory goes under `<data dir>/.panel-update/preflight/<report id>/`.
    Keep only the latest one.
  - `docker compose config --format json` and `config --hash '*'` run in the read-only
    helper:
    - `--project-directory` is the real Install directory, never the scratch directory,
      so relative paths in the operator's files still resolve;
    - the scratch files are passed as `-f`, in label order;
    - the candidate `.env` is passed as `--env-file`.

    See spec Preflight step 4 for the spike.
- Test: an override with `env_file: ./x.env` and a `./foo:/foo` mount passes Preflight,
  and its Service is not listed as recreated when nothing else changed.
- Invariants: one evaluator per kind in the spec's table. An unknown kind gives *Needs a
  manual update*. For a failing invariant, find the file it comes from by re-running
  `config` without the Install override.
- `POST /api/panel-update/preflight` returns the report and its id.
- Tests:
  - table tests per invariant kind over recorded `config` JSON;
  - merge: not edited, edited and merged, edited with a conflict;
  - an override that replaces Traefik's `command:` fails `service-command-has`, and the
    failure names the override;
  - a panel Service with `build:` fails `service-image`.
