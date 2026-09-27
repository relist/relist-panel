# 13 — The graph map: every scene as a card, and the orphans it exposes

> **Deferred — 2026-09-27.** Scoped but not started: the one question a whole-registry view answers that
> the board and the task flows cannot is *reachability* — "does every state have a way in?". 11's task
> flows show the journeys that exist; this shows the ones that do not. Small enough to sit on the
> existing engine: the board's own name card *is* the node rendering this needs, so the map is `Board`
> with the LOD floor above `maxZoom` and the flow layer on.

> **Landed — 2026-09-27.** Built the same day it was scoped; the B-suffix below is the as-built
> wording. Two things came out differently from the scoping note: the orphan report had to be *three
> lists*, not two (see `src/scenes/orphans.ts` — the registry links only `opens`/`tab`, so "no link in"
> is not "unreachable" and the report refuses to imply it), and the map exposed a real board bug — the
> mount-time fit was clamped by `minZoom` and landed on a corner of the board (`281c97a`, recorded in
> 12).

**What to build:** `#/design/map`, a cards-only canvas of the whole registry plus the reachability
report:

- `Board` with `lodBreak: Number.POSITIVE_INFINITY` (a name card at every zoom, zero live content), the
  flow layer on, captions off. The engine needs no new node type — that is the point of the ticket.
- `orphanReport(scenes)` — pure: a scene with no incoming declared link and no menu target (a screen
  whose group no menu item names, like the Compose and Volume Explorer columns, is *menu-unreachable*
  by design and is reported differently from a state nothing links to at all). Covered by `node --test`.
- The report renders beside the canvas; hovering a row highlights the card.

**Blocked by:** 11 (the route/menu wiring pattern), 12 (the engine change lands first so this builds on
the frozen engine).

**Status:** ready-for-human

- [ ] `#/design/map` renders every registry scene as a name card at 100 % zoom, with no live content
      mounted at any zoom
- [ ] `orphanReport` distinguishes "no link in" from "no menu target", and the test pins both
- [ ] The flow layer's toggle still works and the `?scene=` deep link lands on a card
- [ ] `node --test` and `pnpm --dir web-design typecheck` green

- 2026-09-27 — **Landed and accepted.** Commit `523da20` (the fix for the fit is `281c97a`). The map is
  `Board` with `lodBreak: Infinity`; the page adds the surface chrome and the panel, and nothing else —
  no node type, no layout, no canvas code. `orphanReport`'s as-built shape is three lists: overlays
  with no trigger (the actionable one — **empty today**, asserted in `scripts/registry.test.mjs`),
  everything else with no link in (62 scenes, listed with the reading stated: entry screens are reached
  by route or menu, forced states are states), and the groups no menu item names (Compose and Volume
  Explorer, by design). Headless Chrome over the offline build: at rest `105/105 cards` mounted with
  `[data-frame-content]` **0**; after eight ctrl+wheel zoom steps, `22/105 cards` and still **0**
  content. The `?scene=` deep link still lands (checked on the board), and the panel's tags were
  probed (`data-map-report`). <br> The map also exposed that the board's own mount-time fit had never
  worked — `minZoom` 0.08 clamped the ~0.024 fit and left the camera on a corner; lowered to 0.02 and
  recorded in ticket 12.
