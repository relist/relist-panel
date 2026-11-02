# 09 — One updater command and the Install script

Status: closed
Blocked by: 08

Spec: [../spec.md](../spec.md). ADR: [0025](../../../docs/adr/0025-one-updater-and-install-script.md).
Plan: [../plan.md](../plan.md).

## Done

- `relist-panel update --preflight | --report` does the whole Panel update; the web and
  `install.sh --update` start it the same way.
- `relist-panel setup` writes the Install file and `.env` for a new install.
- `release/install.sh` installs from a release; the release workflow fills in the version
  and the image digest and attaches it.
- The panel answers the Updater's quiesce request through `panel.json`; the page shows
  Waiting and Cancel, and an Aborted result.
- `task probe:panel-update` covers install, a refused second install, an update that
  waits for a Deployment, a cancel, a rollback and an update with the panel stopped.
