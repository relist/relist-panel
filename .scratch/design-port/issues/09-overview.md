# 09 — Overview

**What to build:** The design's Overview screen (GUIDELINES §4.3): the four stat cards, the "needs
attention" list derived from the composes, the all-composes table, and `Refresh statuses`. Needs a
flat "all composes" read — the mock loops over Projects and Environments, and so can this, unless an
endpoint is added. The Activity card is deliberately omitted (§11: nothing is recorded).

**Blocked by: 01, 02, 03, 04, 06**

**Status:** closed

- [x] Stats match the design's definitions (Composes = stacks, Containers = running/total, attention =
      partly running or stopped-after-success)
- [x] The needs-attention list opens the Compose it names, one row per degrade
- [x] The all-composes table sorts and links; "Last edited" and "Last deployed" are relative times
- [x] `Refresh statuses` re-reads container status and shows its own loading state
- [x] Empty state when the panel has no Projects yet, as designed

## Comments

Implemented in this commit (`feat: build the Overview screen`).

- `pages/OverviewPage.tsx` ports the prototype's Overview onto the real store, minus the Activity
  card (§11 — the panel records no events), which leaves the attention card full-width. Screen
  decisions live in a pure module, `pages/overview-view.ts`: `flattenComposes` (the mock's
  client-side Projects → Environments → Composes loop — no endpoint added), `overviewStats` (the
  four cards' definitions: Projects, Environments, Composes with a `never deployed` hint, Containers
  running as `running / total`), `needsAttention` / `attentionRows` (partly running, stopped after a
  successful deploy, or an unhealthy Service) and `degradationNotes` (names exited/unhealthy
  Services, tells the stopped story), plus the table's comparators. 18 new tests, 88 total.
- The needs-attention list keeps one row per degraded Compose with a single `Open` (and the banner's
  `Review` opens the first); each row carries its status tag plus the degrade notes. The all-composes
  table sorts on all five columns and every row links: click-through lands on the named Compose.
  "Last edited" is relative everywhere; "Last deployed" renders `RelativeTime` from the
  design-only `lastDeployedAt` and, until the API grows it (§11), an honest `—` with a tooltip saying
  the API does not report deploy times yet.
- `Refresh statuses` re-reads container status for every Compose in the tree with the spinner on the
  button (§8 — loading belongs to the control that started it; the table keeps showing what it read
  before), then the stats, banner, attention list and table re-derive from the patched tree. A
  failure surfaces as a message, matching the shell's own header refresh.
- No Projects yet renders the design's `EmptyState` (glyph, title, one-line explanation, and the
  action that fills it — "Create the first Project"); the stats, banner, attention card and table
  are all absent in that state, and the header keeps its `New Project` entry.
- Two deliberate divergences from the prototype, both ticket-driven: `needsAttention` drops the
  prototype's extra `total > 0` guard on stops — that guard would exclude a deploy later torn down
  (total 0, deploy timestamp present), which the ticket's own definition counts; and the sort
  comparators are natural ascending so antd's direction toggle and the `descend` indicator agree
  (the prototype baked direction in; the baked form displayed oldest-first under a descend arrow).
  "Last edited" opens the table newest-first via `defaultSortOrder: 'descend'`; "Containers" and
  "Last deployed" first-click into their useful direction (most running, most recent).
- The render check caught a real bug the unit fixtures had masked: the API omits the design-only
  `services` array, and the attention derivation read it unguarded — the helpers now decide from the
  counts when services are absent, with a test pinning that shape.
- Verification: `pnpm --dir web typecheck`, `test` (88), `build` and `go test ./...` green; the
  embedded dist rebuilt. Render checks ran the built bundle through the file:// CDP harness
  (route-shaped path under the build's base + `Page.setDocumentContent`, a stateful fetch shim, one
  Chrome per scenario): dashboard (no breadcrumb, the four cards with correct counts and hints,
  "2 Composes need attention" with `Review`, attention rows `api · 1/2 · Partly running` and
  `worker · 0/3 · Stopped · Stopped after a successful deploy`, the flat table with per-row tags,
  three `—` deploy cells and one relative one), sorting (opens newest-first; a `Last edited` click
  flips to oldest-first; `Containers` clicks into most-running-first), `Refresh statuses` (four
  status GETs, the healed Compose flips to `2/2 · Running`, the banner drops to "1 Compose needs
  attention", no error), attention `Open` and a table-row click each landing on the named Compose
  with the right breadcrumb, and the no-Projects empty state. No scroll containers added; the page
  scrolls as one.
- For a human: the shell header's attention badge (ticket 04) still counts partly-running only,
  while this screen counts partly-running + stopped-after-success + unhealthy — §11 calls the badge
  derivable and leaves the definition open, so the two were not silently aligned; worth one decision
  when the Services endpoint lands. `Last deployed` stays a `—` column until the API returns a
  deploy timestamp.
