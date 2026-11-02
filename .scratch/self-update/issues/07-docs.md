# 07 — README, GUIDELINES and first manifest's invariants

Status: closed
Blocked by: 01

Spec: [../spec.md](../spec.md) — "One-time manual upgrade".

## Do

- README:
  - the one-time manual upgrade;
  - the Install file versus override and `.env` rule;
  - what Panel update does not promise: no downgrade, a Traefik restart drops Domains,
    `--env-file` Installs are refused.
- Convert every note under "Upgrading an existing install" into
  `release/invariants.json` and `release/manual-steps.json`. Then shorten that README
  section to a pointer.
- `docs/GUIDELINES.md`: add section 4.15 *Panel update* for the page, with its states.
