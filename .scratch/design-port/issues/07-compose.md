# 07 — Compose as its own route (Editor tab)

**What to build:** The design's Compose screen (GUIDELINES §4.6) as `…/compose/:composeId`: the
`Editor | Monitor` tab row (Monitor's content is 06 in the monitoring spec), Monaco on the left and
the status rail on the right in document flow, both stretched to the same height with
`LAYOUT.editorMinHeight` as the floor; the unsaved-changes banner and Save/Discard; the full-screen
editor modal (`FullscreenEditor`) sharing one Monaco model; the Logs drawer; Deploy identity,
Environment and the deploy/stop/restart/delete actions. Drop the Activity card (no events are
recorded — §11).

**Blocked by: 01, 02, 03, 04, 06**

**Status:** closed

- [x] Editor and rail stretch to the same height; no nested scrollers on the page (GUIDELINES §6); the
      tab row's path segment is linkable and the unknown-segment case falls back to Editor
- [x] Editing marks the file dirty (tag, `Modified` marker, banner); Save persists; Discard reverts;
      deploying saves first
- [x] The full-screen modal opens over the same model and closing it does not discard the draft
- [x] The logs drawer filters by service and copies
- [x] Deploy/stop/restart/delete work, with the deploy-identity card showing the frozen path and
      `docker compose -p` name (ADR-0005)
- [x] No feature lost against the monolith's `ComposeDetailPanel`

## Comments

Implemented in this commit (`feat: rebuild the Compose screen`).

- `pages/ComposePage.tsx` ports the prototype's Compose page onto the router and the real store:
  `PageHeader` (name, status tag, `Unsaved changes` tag, `Logs` / `⋮` / `Save` / `Deploy` graded by
  weight, `Deploy` reading `Save & Deploy` while dirty), the `Editor | Monitor` tab row whose
  segment lives in the path, and the `Row`/`Col` editor + status rail in document flow — no
  `align="top"`, so the columns stretch and the editor takes its height from the rail with
  `LAYOUT.editorMinHeight` as the floor. Screen decisions live in a pure module,
  `pages/compose-view.ts`: `activeComposeTab` (unknown segment → Editor), `hasUnsavedChanges`,
  `deploySteps` (a dirty file saves before the deploy hits the host), `filterLogLines` (service
  prefix filter, last-N trim) and `composeLinkIsStale` (a URL naming parents the Compose does not
  live in redirects to the canonical path, tab included; an unknown Compose is a not-found instead);
  14 new tests, 52 total.
- `components/FullscreenEditor.tsx` is the prototype's component verbatim, re-based onto
  react-router (`useNavigate` + `app/paths`): 100vw/100vh modal, 56px toolbar (filename, trail,
  status tag, `Modified`, Discard, Save, close, and the standing line that closing does not
  discard), the shared Monaco model by `path` with `keepCurrentModel`, and the 340px variables rail
  whose rows insert `${KEY}` at the cursor with reveal/copy and the already-referenced check.
- The status rail shows what the panel knows today: overall tag + `Checked N minutes ago`
  (client-stamped, §11), a per-Service row list gated on the design-only `compose.services`
  (guarded `?? []` — the API omits the key), the design's `Deploy now` empty state at
  `total === 0`, the partial warning alert, and an honest note where rows would be
  ("Per-Service rows arrive with the panel's Services endpoint") so host-monitoring ticket 06
  replaces exactly that branch when its endpoint lands. The logs drawer's service filter is fed
  from the same array.
- Deploy identity (ADR-0005) renders the frozen stored identity —
  `dataDir/{projectSlug}/{envSlug}/{name}` and `docker compose -p {projectSlug}-{envSlug}-{name}`,
  both copyable — read from the Compose row, never re-derived from current parent names (the
  prototype mocked names; slugs survive a rename). Delete's `DangerModal` copy names the same
  frozen path.
- Monolith parity (`git show d11786a^:web/src/main.tsx`): save, save-and-deploy, restart, stop,
  delete (now behind the typed-name modal, up from `Popconfirm`), on-demand logs, error surfacing
  as an on-screen `Alert` (the monolith's error Alert kept, per the ticket-06 pattern), a loading
  card for deep links landing before the tree loads, and the rail's refresh button replacing the
  monolith's `Refresh` (re-reads container status, re-stamps "checked"). The monolith's back
  button is gone by design (§1.7): the breadcrumb and post-delete navigation carry the way back.
  Activity card dropped deliberately (§11; the API records no events).
- Verification: `pnpm --dir web typecheck`, `test` (52), `build` and `go test ./...` green; the
  embedded dist rebuilt. Render checks ran the built bundle through the file:// CDP harness
  (single-file inlined build, method-aware fetch shim over fixtures, route-shaped file paths so
  `location.pathname` equals the route, real CDP input events): Editor tab baseline (breadcrumb,
  `2/2 · Running`, service rows, identity card, Environment card), typing marks dirty (tag +
  `Modified` + banner + `Save & Deploy`), deploy on a dirty file (PUT `/composes/1` before POST
  `/deploy`, banner and tag clear, `Deployed web`), fullscreen modal over the dirty draft (draft
  carried, `Modified`, closing via Esc keeps the banner and `Modified`), Insert from the
  variables rail (the row's already-referenced check renders — executeEdits reached the shared
  model and the draft state), logs drawer (4 of 4 lines → filtered to `web`: 2 of 4, only `web-1`
  lines, copy confirmation, Download and Follow visible-but-disabled), ⋮ menu (Restart/Stop
  disabled at `total === 0`; Stop then Restart hit the API and name the Compose), delete
  (consequences copy with the frozen path, typed name enables the button, lands on the
  Environment page), unknown tab segment → Editor, Monitor tab active with its placeholder,
  never-deployed rail (`Not deployed` + `Deploy now`), counts-only rail (the API's real shape:
  `1/3 · Partly running`, partial alert, per-Service note), and a stale link rendering the
  canonical Compose.
- Deviations, deliberate: the variables load fires on page mount (the tree ships variables
  unloaded; the Environment card and the fullscreen rail read them — a failed load joins the
  screen's error Alert); action failures render the closable on-screen `Alert` rather than the
  prototype's unhandled rejections (GUIDELINES §8, ticket-06 pattern); the logs drawer uses the
  store's `logs:<id>` pending key instead of the prototype's local loading flag.
- Harness notes, for the next agent: Chrome's network service cannot reach local http here (even
  `127.0.0.1`), so the technique is route-shaped *file* paths under the build's `base` plus
  `Page.setDocumentContent` — one Chrome process per scenario, since navigating a synthetic
  document twice reverts it to `about:blank`. Monaco's view layer renders once and does not
  repaint programmatic edits in this environment (workers can't load, and the EditContext input
  path repaints only real input), so the insert is asserted at the state level (the row's
  referenced check) rather than through the editor's DOM text.
- For a human: `Compose.lastDeployedAt` and the `services` array are still unknown to the API
  (§11) — a Compose stopped after a deploy reads "Not deployed", the rail shows counts instead of
  Service rows, and the log filter's option list is empty until host-monitoring ticket 06 lands
  the Services endpoint; `secret` is likewise unmarked in the fullscreen rail until ticket 08.
