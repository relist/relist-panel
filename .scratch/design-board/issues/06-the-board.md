# 06 — The board: one canvas, frames in menu order

**What to build:** `#/design/board` — a route in the mock that renders the registry (04) and the
manifest (05) as a pannable, zoomable canvas of stills. It renders **outside `AppShell`**, exactly as the
gallery it replaces does, because the shell is part of what each frame is showing.

Layout grammar, from the spec's `### Layout grammar`:

- one column per `SceneGroup`, left to right in menu order, with a header per column;
- inside a column, frames stack in registry order, and a screen's overlays sit in an indented **rail to
  its right**, so "Compose — running" and the three dialogs that belong to it read together;
- a **mobile frame sits in a second rail**, one rail further right of the desktop frame it pairs with,
  labelled with its viewport — desktop and phone read as a pair, and the device filter (08) removes
  whichever half the reviewer is not asking about;
- every frame is laid out at its captured pixel size from the manifest, so nothing reflows as images
  load — the sizes are in the manifest for exactly this reason;
- a scene's tabs sit with the screen they belong to, and `feedback` scenes form their own column.

Frame chrome, under each frame: scene **id** (monospace, copyable — it is the string feedback quotes),
title, route, caption, the viewport for a mobile frame, and **Open live** (the `?ui=` + hash URL from
01/02). Clicking the image opens a lightbox at 100 %; the frame itself never navigates.

Holes are visible, never silent: a scene with no still renders a labelled placeholder frame, and because
there are no dark stills, a missing *device* frame is a hole too — never a desktop frame quietly standing
in for a phone one.

Pan and zoom: wheel zooms to the cursor, drag pans, `Fit` frames a group, `0` / `+` / `-` for the
obvious, `100 %` for the truthful. A minimap is a stretch, not part of the plan.

The layout module is render-free (`web-design/src/board/layout.ts`): registry + manifest in, frame and
rail boxes out. It is what 07 draws lines against, and it is testable without a browser.

**Blocked by:** 04, 05

**Status:** needs-triage

- [ ] The route renders bare (no shell), the menu's Design system group links it, and it is reachable
      after a cold load with no query string
- [ ] Every registry scene has a frame; the count matches the registry, and a scene added to the registry
      without a still shows the labelled placeholder rather than a blank area
- [ ] Frames are placed at their manifest sizes, so the canvas does not reflow when the images finish
      loading
- [ ] Overlays sit in their screen's rail, in registry order; `menuPath` is the only thing that decides
      position (no hand-placed coordinates anywhere in the board)
- [ ] Frame chrome carries id, title, route, caption and Open live (plus whatever open question 2 settles)
- [ ] Clicking a frame opens the lightbox at 100 %; the frame does not navigate
- [ ] **Open live lands on the frame's exact state** — the acceptance test for the whole plan: open the
      light window, paste three different `Open live` URLs, and each renders the state its caption claims
- [ ] The manifest is **imported**, not fetched: the board renders with the network panel showing no
      request for it (this is what makes 08's offline path work)
- [ ] `src/board/layout.ts` is pure and covered by `node --test scripts/board-layout.test.mjs` — column
      and rail placement, ordering by `menuPath`, and a frame's position for a known registry
- [ ] `pnpm --dir web-design typecheck` green; no new dependency
