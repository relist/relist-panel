# 08 — Install file uses the published image

Status: closed
Blocked by: 02 (a first release exists on GHCR), 05

Spec: [../spec.md](../spec.md) — "Install file and image pin", "Once releases exist".

## Do

- In `docker-compose.yml`, the panel Service:
  - becomes `image: ${RELIST_PANEL_IMAGE:-ghcr.io/relist/relist-panel:latest}`;
  - loses `build: .` and `pull_policy: build`.
- Add `docker-compose.dev.yml` with `services.panel.build: .`,
  `image: ghcr.io/relist/relist-panel:local` and `pull_policy: build`, for local and from-source runs.
- Add `release/invariants.json` with `service-image` for the panel, if ticket 07 has not
  already.
- README:
  - the one-time manual upgrade (spec);
  - the development command
    `docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build`.

## Watch out

The live install runs from this checkout and is rebuilt from feature branches. After
this merges, a plain `docker compose up -d` there runs `ghcr.io/relist/relist-panel:latest` from GHCR,
not the branch. Rebuilding a branch needs the dev command above. Say so in the PR
description.

## Comments

Closed by the one-updater rework (task 1 of `.scratch/self-update/plan.md`): the Install file runs
`${RELIST_PANEL_IMAGE:-ghcr.io/relist/relist-panel:latest}`, names its Containers, and the build
block moved to `docker-compose.override.example.yml` (instead of a `docker-compose.dev.yml`). The
README has the existing-Install steps.
