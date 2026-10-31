# 06 — Web: Panel update page

Status: ready-for-agent
Blocked by: 03 (status states), 04 (Preview), 05 (Updating onwards)

Spec: [../spec.md](../spec.md) — "Web".

## Do

- Read Ant Design's `llms.txt`, then the pages for the components used, checked
  against the `antd` version in `web/package.json`. Follow `docs/GUIDELINES.md`.
- Add the route `/panel-update`, a side-menu entry below Docker, and the header
  `Update available` tag.
- Build every state in the spec's "Page states" table. Polling follows the spec's
  "Polling" rules: a network error or 5xx is Restarting, a 401 goes to sign-in, and
  matching the target version reloads the page once.
- Preview:
  - the merge diff, read-only Monaco diff;
  - the invariant list with pass/fail and the file to blame;
  - the Services to be recreated, with the Traefik warning;
  - the manual-step checkboxes.
- Confirm dialog with the text from the spec.
- Rebuild the embedded frontend.
