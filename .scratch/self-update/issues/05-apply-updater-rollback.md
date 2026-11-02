# 05 — Apply: quiesce, backup, Updater, rollback

Status: closed
Blocked by: 04

Spec: [../spec.md](../spec.md) — "Apply".

## Do

- Update lock, driven by the newest run's `status.json`. It has three parts:
  - write middleware with the spec's allowlist, shaped like the Migration lock's
    `allowedWhileLocked` (`internal/migration/lock.go`);
  - `QueueControl.Hold`;
  - a scan-queue gate next to the Migration-lock gate in `internal/vulnscan`.
- Start locked: at startup, before HTTP and the workers start, a non-terminal
  `status.json` turns the lock on.
- Quiesce:
  - write `status.json` with state `quiescing`;
  - `WaitIdle`;
  - wait for the scan queue's current task;
  - refuse while a Migration lock is held.
- Watcher:
  - a terminal state releases the lock;
  - non-terminal with the Updater Container gone or exited marks the run
    `interrupted` and releases the lock.
- Health address: the IP of the Install's `traefik` Container on `relist-network`, found
  by Compose labels. Never the DNS name `traefik`.
- Backup to `<data dir>/.panel-update/<id>/`. Make the `panel.db` copy with
  `VACUUM INTO`, the same way `internal/migration/items.go` does.
- Start the Updater with `docker run` as the spec shows: no Compose labels, named
  `relist-panel-updater-<id>`.
- Add `relist-panel panel-update apply --run <id>`, dispatched in `main.go` beside the
  other subcommands. It does steps 4.1–4.5:
  - `status.json` is written atomically (temp file and rename) before and after each
    step;
  - the health wait is 180 s, polling every 2 s;
  - rollback stops the panel, restores `panel.db`, then runs `up -d`.
- On startup, read the newest `status.json` into `panel_updates`, then remove the exited
  Updater Container.
- Prune plans: the previous panel image and `.panel-update/` are owned by "Panel update"
  and are never offered for removal.
- Tests, with a fake Docker and filesystem:
  - success;
  - health timeout leads to rolled back;
  - the restore itself fails, giving rollback failed;
  - a stale Updater that is still running is refused;
  - writes return 503 while locked;
  - a new process with a non-terminal `status.json` starts locked and runs no
    Deployment;
  - a dead Updater gives interrupted, and the lock is released;
  - `/healthz` and `GET /api/panel-update/status` pass while locked.
- Manual check on the dev host, recorded in the PR:
  - update between two locally built images;
  - one run forced to fail health, which must roll back;
  - one run with a Traefik change, which must show the warning.
