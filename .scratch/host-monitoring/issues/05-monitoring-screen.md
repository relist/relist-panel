# 05 — Host Monitoring screen

**What to build:** Port `web-design/src/pages/MonitoringPage.tsx` into the real app at `/monitoring`:
the six charts (CPU, memory, disk space, Docker disk usage, block I/O, network I/O) over the
15m/1h/6h/24h windows, reusing `MetricChart` — which is hand-rolled inline SVG with no chart
dependency, so it comes across as-is — plus the sampling-budget caption and the nav item after
Projects.

**Blocked by: 04 (this feature), and 04 + 07 of the design port (shell and data layer)**

**Status:** open

- [ ] The six charts render from the real endpoint, with the design's units, fixed 0–100 axes for the
      percent metrics and auto-scaling for the byte rates
- [ ] `partial` shows "collecting — N of M minutes" after a restart; an unavailable source shows the
      reason from `sources`, never a zeroed line
- [ ] Docker disk usage renders as the step it is (a cached refresh, carried forward)
- [ ] The chart downsamples to the plot's pixel width keeping min and max, with a vitest test for the
      rule (it ships with `MetricChart` from the prototype)
- [ ] Page scrolls with the document, no nested scrollers, and the window control is linkable state
- [ ] Verified the way the prototype was: typecheck, build, DOM probe at 1600/760, screenshots

## Comments
