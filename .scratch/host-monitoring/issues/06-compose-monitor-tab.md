# 06 — Compose Monitor tab

**What to build:** Port `web-design/src/components/ServiceMonitor.tsx` and the `Editor | Monitor` tab
row of the Compose screen: a service picker fed by the Services endpoint, the window control, the
facts strip (image, container ID, state, PIDs, last deploy, and the line explaining why history is
keyed by Service), and the four per-service charts (CPU on an auto axis because a container can exceed
one core, memory against the container's own limit, network I/O, block I/O) with the deploy marker.
The status rail also gains its per-Service rows from the same endpoint.

**Blocked by: 04 (this feature), and 07 of the design port (the Compose screen)**

**Status:** closed

- [x] The service picker lists the Compose's Services with their state; selecting one changes every
      number on the screen
- [x] The deploy marker appears on all four charts when the last deploy falls inside the window, and
      is absent when it does not
- [x] A Compose that has never been deployed shows the empty state pointing at the Editor tab
- [x] The rail's per-Service rows show name, state, health, image and ports from the same endpoint
- [x] Verified as in 05, plus a real-input interaction test for the picker (a synthetic click does not
      open antd v6's Select — drive it through CDP input events)

## Comments

Implemented in this commit (`feat: add the Compose Monitor tab`).

- `components/ServiceMonitor.tsx` is the prototype's component re-based onto the real store: the
  service `Select` (state dot + mono name + image per option), the window `Segmented` (default 1h,
  view state — the tab itself is the linkable part, per the spec), `Refresh`, the facts strip
  (`ServiceStateTag`/`HealthBadge`/`CodeValue`/`RelativeTime`, container ID shown short and
  copyable — Docker's 64-hex form would blow the strip out), the PIDs reading off the last sample,
  the keyed-by-Service line verbatim, and the 2×2 `MetricChart` grid from
  `serviceChartSpecs` (chart math already tested with ticket 05's module: CPU `axis="auto"`, memory
  against the container's own limit, `dockerStats` unavailable → all four charts say so).
- Data layer: `api.ts` gains `listServices` and `serviceMetrics` (service name URL-encoded);
  `types.ts` gains the `ServiceFacts` wire shape, `serviceFromFacts` (wire → screen row, unknown
  states/healths fall back to the vocabulary's neutral members), `formatPort` (docker compose ps
  form `8080->80/tcp`, wildcard IPs dropped, IPv6 bracketed) and `shortContainerId` — tested.
  The design-only `Compose.services` field is deleted from the `Compose` type: the store holds
  per-Service rows in a `services` map keyed by Compose (the API's Compose carries no services key,
  and tree patches would wipe it), shared by the rail, the picker and the logs filter.
- The deploy marker reads `deployedAt[compose.id]`, a stamp the store records on a successful
  deploy — the API returns no deploy timestamp (GUIDELINES §11; same client-side treatment the
  status read time already gets). No stamp, no marker; a stamp outside the chart's window is
  dropped by the chart's marker predicate (tested). The facts strip says "Last deploy not recorded"
  rather than inventing a time.
- `ComposePage`: the Monitor tab renders `ServiceMonitor` (the placeholder is gone); the status
  rail's rows come from the same endpoint and the counts-only branch ticket 07 left is replaced
  (rows → the empty "Deploy now" state at `total === 0` → a "collector has not read these containers
  yet" note for the 30-second sweep gap). Rows load once the deep link resolves its Compose (the
  tree ships later than the page — the same gate the variables load uses), on the rail's refresh
  button and after a deploy. The logs drawer's service filter feeds from the same rows.
- Verification: `pnpm --dir web typecheck`, `test` (94, +4), `build`, `go test ./...` — green, dist
  rebuilt. Render checks ran the built app through the file:// CDP harness at 1600/760: 21 checks —
  the rail's rows (names, state tags, health badges, images, `8080->80/tcp` ports, counts-only note
  gone); the Monitor tab (facts strip with image, 12-char container ID, PIDs, keying rule; picker on
  `web`; four charts; CPU auto-scaled to 200% with the 150% spike kept while memory stays on 0–100
  against the 2 GB limit; no marker before a deploy); the picker driven by real CDP
  `Input.dispatchMouseEvent` — open, both options with state dots and images, pick `worker`, and
  every chart footer plus the facts strip (name, image, container ID, PIDs) changes; a session
  deploy stamping `Deployed` markers onto all four charts live, with the strip flipping to
  "Deployed a few seconds ago"; the never-deployed Compose's "Nothing to monitor yet" empty state
  whose action navigates to the Editor path; and the never-deployed rail ("Not deployed" +
  `Deploy now`, no rows). Screenshots: `06-rail`, `06-monitor-web`, `06-monitor-worker`,
  `06-deploy-marker`, `06-never-deployed`, `06-never-deployed-rail`.
- Deviations and notes, deliberate: the prototype's empty-state guard was `!service ||
  !deployedAt`; the real Services endpoint only lists containers the collector can see (running
  ones), so "never deployed" and "fully stopped" both arrive as an empty list and share the empty
  state — its copy says deploying starts collection, which covers both; the deploy stamp is
  session-only (a reload loses it) until the API records deploy times; `state`/`health` values
  outside the vocabulary render as `created`/`none` rather than crashing the tag maps.
- Harness notes for the next agent: antd v6's Select opens fine under real CDP mouse events (the
  synthetic-`.click()` warning applies to simulated clicks only); read the selected value from
  `.ant-select-content` (v6 has no `.ant-select-selection-item`), and let the dropdown's open
  animation settle (~500 ms) before reading option geometry. In-page `navigate()` to a different
  path under file:// turns into a real navigation (the harness's one artifact), so tab-switch
  assertions read the URL, not the re-rendered tree.
- For a human: the API still records no deploy timestamp (§11) — "last deploy" and the marker are
  session-scoped until the backend keeps one; a Compose stopped after a deploy shows "Not deployed"
  and an empty Monitor tab (no running containers to list), while its history survives in the
  rollups for when it runs again.
