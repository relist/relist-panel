# 06 — Project, reshaped

**What to build:** The design's Project screen (GUIDELINES §4.4): an Environment `Segmented` (with a
"manage" affordance for create/delete) and `Composes | Variables | Settings` tabs in the path
(`/projects/:p/env/:e/:tab`), replacing the monolith's nested tabs. Includes Compose create/delete and
the Environment's Compose list.

**Blocked by: 01, 02, 03, 04**

**Status:** closed

- [x] The Environment segmented switches environments; the tab is in the URL and survives a reload
- [x] The Composes tab lists each Compose with the status vocabulary, last deploy, and the design's
      row actions
- [x] Create/delete Compose and create/delete Environment work, with `DangerModal` for the
      destructive one and its warning copy (containers, networks, volumes, files under a named path)
- [x] Empty states are the design's, not antd defaults
- [x] No feature lost against the monolith's Project detail screen

## Comments

Implemented in this commit (`feat: reshape the Project screen`).

- `pages/ProjectPage.tsx` ports the prototype's Project page onto the router: `PageHeader` with the
  status rollup and Environment/Compose counts, the Environment `Segmented` with per-Environment
  Compose counts and the active Environment's container rollup on the right, and the
  `Composes | Variables | Settings` tab row whose segment lives in the path. Unknown tab segments
  fall back to Composes (`pages/project-view.ts`, pure and tested: `activeTab`, `selectEnvironment`
  — the URL's Environment when it exists, else the first — and the Projects list's
  `visibleProjects`; 9 new tests, 38 total). A deep link that lands before the tree loads shows a
  loading card; a stale one shows the design's not-found.
- The Composes tab is fully real: the table (Compose, dot-form status vocabulary, frozen deploy
  identity `projectSlug-envSlug-name`, last edited) with per-row Open, one-click Deploy/Stop and the
  ⋮ menu (Deploy disabled while running, Restart/Stop disabled at total=0, Delete). Row click opens
  the Compose route. The Variables and Settings tabs render the design's empty-state shape with a
  pointer to their ticket — Variables with 08; Settings (Project form, Environment list, danger
  zone) has no ticket yet, so its placeholder says where its jobs live meanwhile.
- CRUD, all probed in the render harness: New Environment (header button, empty state and the
  switcher's manage affordance all open one modal, name + suggestion tags), New Compose (name +
  Monaco YAML with the starter example and the Traefik note; `YamlEditor` ported verbatim from the
  prototype for it; creating navigates to the new Compose's URL, which is ticket 07's placeholder
  for now), Delete Compose and Delete Environment behind `DangerModal`s, Project create/edit/delete
  on the Projects list (`pages/ProjectsPage.tsx`, ported from §4.3 because its placeholder named
  this ticket and Project CRUD must exist somewhere — the monolith kept it on the list too).
- Honest-copy findings against the real API (the prototype was mock-only): only Compose delete
  touches the host (`docker compose down -v` + `RemoveAll`), so its `DangerModal` keeps the design's
  warning verbatim; Environment and Project delete remove *records only* (files stay on disk,
  containers keep running), so their copies say exactly that instead of the prototype's "stops and
  removes". Project delete also keeps the typed-name modal (upgrading the monolith's Popconfirm per
  GUIDELINES §1.6) with the corrected consequences.
- Deviations, deliberate: the manage affordance (a `Manage` dropdown beside the Segmented: New
  Environment… + per-Environment delete) exists because this ticket must ship Environment
  create/delete while the Settings tab that would normally hold them is deferred; action failures
  render a closable on-screen `Alert` rather than the prototype's error toasts (GUIDELINES §8); the
  row action buttons stop the row's click-to-open so one-click Deploy/Stop stays on the list
  (prototype let the click bubble through to navigation); Project delete copy drops the prototype's
  inaccurate `dataDir/{name}` teardown claim (see above).
- Verification: `pnpm --dir web typecheck`, `test` (38), `build` and `go test ./...` green; embedded
  dist rebuilt. Render checks ran the built bundle through the file:// single-file harness for:
  the Projects list (cards + rollups) and its create modal end-to-end (`Created my-saas-2`), the
  Project screen with two Composes (Running 2/2 and Partly running 1/3), Deploy from the row
  (rollup flips to 5/5 · Running, one-click action becomes Stop, stays on the list), Compose delete
  (typed-name modal, row gone, rollups update), Manage → New Environment (`preview` joins the
  Segmented), Manage → Delete production (typed-name modal, Environment gone, falls back to staging
  with the design's empty state), New Compose modal (editor renders, example inserts, create
  navigates), the Variables/Settings placeholders, an unknown tab segment falling back to Composes,
  and sign-out from the shell. Screenshots reviewed against the design. Not reachable in fixtures: a
  Project with zero Environments (the API provisions a default one, per ticket 03) — that empty
  state ships but is exercised only by code.
- For a human: the Settings tab has no ticket (08 is Variables, 09 Overview) — someone should cut a
  ticket for the Project details form, the Environment list and the danger zone, and retire the
  `Manage` affordance's delete half when it lands. `Compose.lastDeployedAt` is still unknown to the
  API, so a Compose stopped after a deploy reads "Not deployed" until that gap closes (§11).
