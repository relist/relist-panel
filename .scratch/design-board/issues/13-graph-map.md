# 13 — The graph map: every scene as a card, and the orphans it exposes

> **Deferred — 2026-09-27.** Scoped but not started: the one question a whole-registry view answers that
> the board and the task flows cannot is *reachability* — "does every state have a way in?". 11's task
> flows show the journeys that exist; this shows the ones that do not. Small enough to sit on the
> existing engine: the board's own name card *is* the node rendering this needs, so the map is `Board`
> with the LOD floor above `maxZoom` and the flow layer on.

**What to build:** `#/design/map`, a cards-only canvas of the whole registry plus an orphan report:

- `Board` with `lodBreak: Number.POSITIVE_INFINITY` (a name card at every zoom, zero live content), the
  flow layer on, captions off. The engine needs no new node type — that is the point of the ticket.
- `orphanReport(scenes)` — pure: a scene with no incoming declared link and no menu target (a screen
  whose group no menu item names, like the Compose and Volume Explorer columns, is *menu-unreachable*
  by design and is reported differently from a state nothing links to at all). Covered by `node --test`.
- The report renders beside the canvas; hovering a row highlights the card.

**Blocked by:** 11 (the route/menu wiring pattern), 12 (the engine change lands first so this builds on
the frozen engine).

**Status:** ready-for-agent

- [ ] `#/design/map` renders every registry scene as a name card at 100 % zoom, with no live content
      mounted at any zoom
- [ ] `orphanReport` distinguishes "no link in" from "no menu target", and the test pins both
- [ ] The flow layer's toggle still works and the `?scene=` deep link lands on a card
- [ ] `node --test` and `pnpm --dir web-design typecheck` green
