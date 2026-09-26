# 06 — The board: one canvas, frames in menu order

> **Re-scoped — 2026-09-27 (spec.md architecture update).** The canvas renders **live inline
> frames**, not stills: no manifest, no holes-with-Live-buttons. Frames are the mock's own screens
> wrapped in ticket 10's `SceneScopeProvider`, rendered through a reusable engine in
> `src/storyboard/` (React + `@xyflow/react` only — it never imports antd, the router or the mock),
> with `onlyRenderVisibleElements` culling and an LOD floor (below ~35% zoom a frame is a name
> card). Acceptance adds what the stills design got for free: while panning the whole canvas,
> mounted frames stay ≤ ~24 and JS heap stays flat (CDP `Performance.getMetrics`), and zero frames
> hit the error boundary. The Live cap, the revision corner and the manifest import are withdrawn;
> the offline `dist-single` path gets *simpler* (inline frames need no dev server). Blocked by 02,
> 04 and 10 — not 05.

**What to build:** `#/design/board` — a route in the mock that renders the registry (04) and the
manifest (05) as a pannable, zoomable canvas of stills. It renders **outside `AppShell`**, exactly as the
gallery it replaces does, because the shell is part of what each frame is showing.

Layout grammar, from the spec's `### Layout grammar`:

- one column per `SceneGroup`, left to right in menu order, with a header per column;
- inside a column, frames stack in registry order, and a screen's overlays sit in an indented **rail to
  its right**, so "Compose — running" and the three dialogs that belong to it read together;
- a **tablet rail, then a mobile rail**, each to the right of the desktop frame it pairs with, labelled
  with its width — desktop, tablet and phone read as one group, and the viewport switch (08) decides
  which rails are on the canvas;
- every frame is laid out at its captured pixel size from the manifest, so nothing reflows as images
  load — the sizes are in the manifest for exactly this reason;
- a scene's tabs sit with the screen they belong to, and `feedback` scenes form their own column.

Frame chrome, under each frame: scene **id** (monospace, copyable — it is the string feedback quotes),
title, route, caption, the width on tablet/mobile frames, **Open live** (the `?ui=` + hash URL from
01/02) and **Live** (below). Clicking the image opens a lightbox at 100 %; the frame itself never
navigates.

Holes are visible, never silent: a scene with no still for the selected width renders a labelled
placeholder frame **with a Live button**, and because there are no dark stills, a missing width is a hole
too — never another width's image quietly standing in for it.

**Live mode, per frame.** The **Live** control swaps that one frame's image for a real iframe of the
mock, at the width the viewport switch is on and the theme the theme switch is on: `?ui=`/`after` come
from the scene, so the live frame is the same state, and the theme flips by posting `relist:theme`
(`main.tsx:85` already listens) rather than reloading. That is what makes a width and a theme that were
never captured reviewable at all — and it is deliberately one frame at a time here (the bar's *Live this
group* / *filtered set*, capped at 8, is 08). Offline there is no dev server, so Live is disabled and
says why instead of showing a blank box. Monaco inside a Live frame needs the `automaticLayout` line from
03, since the frame's width changes under it.

Pan and zoom: wheel zooms to the cursor, drag pans, `Fit` frames a group, `0` / `+` / `-` for the
obvious, `100 %` for the truthful. A minimap is a stretch, not part of the plan.

The layout module is render-free (`web-design/src/board/layout.ts`): registry + manifest in, frame and
rail boxes out. It is what 07 draws lines against, and it is testable without a browser.

**Blocked by:** 04, 05

**Status:** ready-for-human

- [ ] The route renders bare (no shell), the menu's Design system group links it, and it is reachable
      after a cold load with no query string
- [ ] Every registry scene has a frame; the count matches the registry, and a scene added to the registry
      without a still shows the labelled placeholder rather than a blank area
- [ ] Frames are placed at their manifest sizes, so the canvas does not reflow when the images finish
      loading
- [ ] Overlays sit in their screen's rail, in registry order; `menuPath` is the only thing that decides
      position (no hand-placed coordinates anywhere in the board)
- [ ] Frame chrome carries id, title, route, caption, the width on tablet/mobile frames, Open live and Live
- [ ] The viewport switch (08) shows the variant the scene declares, and a width the scene does not declare
      renders the labelled hole with its Live button — asserted in the DOM, because a stretched or borrowed
      image is the exact failure this guards
- [ ] **Live** swaps one frame for a real iframe at the same `?ui=`/`after` state, reflows when the width
      changes, and flips theme through `relist:theme` with **no reload** (the harness counts page loads)
- [ ] Live is disabled offline with a stated reason, and the rest of the board is unaffected by that
- [ ] Clicking a frame opens the lightbox at 100 %; the frame does not navigate
- [ ] **Open live lands on the frame's exact state** — the acceptance test for the whole plan: open the
      light window, paste three different `Open live` URLs, and each renders the state its caption claims
- [ ] The manifest is **imported**, not fetched: the board renders with the network panel showing no
      request for it (this is what makes 08's offline path work)
- [ ] `src/board/layout.ts` is pure and covered by `node --test scripts/board-layout.test.mjs` — column
      and rail placement, ordering by `menuPath`, and a frame's position for a known registry
- [ ] `pnpm --dir web-design typecheck` green; no new dependency
- 2026-09-27 — **Landed and accepted** (commit 585b3ab + the engine fixes). Acceptance, headless Chrome over CDP against the dev server, 8/8: a full pan across all columns keeps **mounted ≤ 4** of 105 frames; zero error-boundary cards; JS heap 154→241 MB across the pan (first-mount allocations, flat afterwards — no trend); the theme switch re-themes the chrome **and** every mounted frame live with zero page loads; `?scene=compose-editor-running` lands centred at 0.6 zoom with the frame mounted; a forced `?ui=compose.logsOpen=1` cold load opens the Logs drawer. Two engine fixes rode along: the LOD predicate compared zoom against the current state twice (frames stuck as name cards mid-pan) and the mount-time focus could silently drop when `setCenter` ran before the pan/zoom surface existed (now retries). Offline verified separately: `dist-single/index.html#/design/board` from disk renders the board, 4 mounted, target frame present, no crashes (ticket 09).
