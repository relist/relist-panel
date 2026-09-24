# 05 — Host Monitoring screen

**What to build:** Port `web-design/src/pages/MonitoringPage.tsx` into the real app at `/monitoring`:
the six charts (CPU, memory, disk space, Docker disk usage, block I/O, network I/O) over the
15m/1h/6h/24h windows, reusing `MetricChart` — which is hand-rolled inline SVG with no chart
dependency, so it comes across as-is — plus the sampling-budget caption and the nav item after
Projects.

**Blocked by: 04 (this feature), and 04 + 07 of the design port (shell and data layer)**

**Status:** closed

- [x] The six charts render from the real endpoint, with the design's units, fixed 0–100 axes for the
      percent metrics and auto-scaling for the byte rates
- [x] `partial` shows "collecting — N of M minutes" after a restart; an unavailable source shows the
      reason from `sources`, never a zeroed line
- [x] Docker disk usage renders as the step it is (a cached refresh, carried forward)
- [x] The chart downsamples to the plot's pixel width keeping min and max, with a vitest test for the
      rule (it ships with `MetricChart` from the prototype)
- [x] Page scrolls with the document, no nested scrollers, and the window control is linkable state
- [x] Verified the way the prototype was: typecheck, build, DOM probe at 1600/760, screenshots

## Comments

Implemented in `9adfb83`.

- `components/MetricChart.tsx` is the prototype's chart verbatim — inline SVG measured off its own
  box, min/max downsampling, crosshair tooltip, `partial`/`unavailable` states — with the pure rules
  exported (`downsample`, `summarizeMetric`, formatters, `axisMax`, `markersInWindow`) so they test
  in the node env, and one small extension: a `step` prop whose polyline holds each value until the
  next sample and jumps there, because a carried-forward series (Docker disk usage) is flat by fact,
  not by coincidence. 19 tests cover the downsample rule (spike preserved, buckets ≤ half the pixel
  width ×2, time order kept), unit formatting, the nice-max axes, summaries, the deploy-marker
  window predicate and the step path.
- `pages/monitor-view.ts` is the screen-decision module, pure and tested: envelope → six
  `ChartSpec`s. It maps the wire `sources` keys onto the chart each may speak for (a chart whose
  source is unreadable renders `unavailable` with the endpoint's own reason and no line at all),
  computes the collecting caption from the sample count and the envelope's `intervalSeconds`
  (`collectedMinutes`), skips absent readings — JSON null becomes no point, never a zero — reads
  `warn` from the *current* reading at the design's 85% threshold, and marks only the Docker chart
  `step`. 19 more tests.
- `pages/MonitoringPage.tsx` ports the design's page onto the real store: window `Segmented` +
  `Refresh`, the sampling-budget caption, six charts (or six skeletons until the window answers).
  The window lives in `?window=` (validated, default `1h`, dropped again at the default) so a chart
  is linkable; the endpoint envelope replaces the prototype's bare sample array, so `partial` and
  the `sources` reasons come from the API instead of being inferred. Load failures render the §8
  Alert with Retry.
- Data layer: `types.ts` gains `MetricWindow`, the two resolution tables, the nullable sample types
  (JSON null ↔ absent reading) and the envelope; `api.ts` a thin `hostMetrics(window)`; `store.tsx`
  a `loadMetrics(window)` returning it under the `metrics:<window>` pending key — screen-local
  state, no tree write.
- The nav item after Projects, the route, and the Monitoring breadcrumb were already in place from
  the design port's shell ticket; this ticket verified them (selected state included).
- Verification: `pnpm --dir web typecheck`, `test` (90, +38), `build`, `go test ./...` — green, dist
  rebuilt. Render checks ran the built app through the file:// CDP harness at 1600/760: 28 checks —
  six cards with the design's titles; per-series points within the plot-pixel budget; fixed 0–100
  labels on the percent charts and a scaled axis on Network I/O; the Docker chart's stroked path
  showing 358 flat holds and exactly one refresh jump while CPU's stays a plain line; `collecting —
  3 of 60 minutes` on all six charts for a short window; an unreadable network source rendering
  `Source not readable` with the endpoint's `/host/proc/1/net/dev` reason and zero SVG paths; the
  budget caption; the sider item after Projects and selected; no inner scrollers; `?window=6h` deep
  link served from the rollups, and a real CDP mouse click on the `24h` segment writing
  `?window=24h` into the URL and refetching (antd's Segmented takes synthetic-free input events
  fine; only the Select dropdown needs them — that is ticket 06's problem). Screenshots:
  `ok-1h`, `partial`, `unavailable`, `window-24h`.
- Harness notes for the next agent: the working recipe is (1) `vite build --base <harness-dir>/`
  into /tmp so a route-shaped path under it IS the route for react-router's basename; (2) a real
  file at each route path (any content — Chrome serves extensionless file:// URLs as text/plain,
  so the app html can never load from disk); (3) navigate there, then `Page.setDocumentContent`
  the index.html with a fetch shim prepended; (4) launch Chrome with
  `--allow-file-access-from-files`, without which file:// module scripts are CORS-blocked and the
  bundle never executes.
- Deviations, deliberate: the prototype's `host.name` in the description is dropped (no endpoint —
  GUIDELINES §11); `partial`/coverage comes from the envelope rather than being recomputed (the
  endpoint's own rule, same numbers); `collecting` shows only on readable charts (an unavailable
  card has nothing to be partial about).
