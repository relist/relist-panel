# 11 — Task flows: the registry's links as the journeys a reviewer walks

> **Split out of 06/07 — 2026-09-27.** The board (06) is an inventory: 105 frames at their real size,
> which is the right artefact for "does this screen look right". It is the wrong artefact for walking a
> journey: at review zoom the canvas is unreadable, and lines drawn between full-size frames cannot say
> *what the user pressed*. This ticket builds the second surface — `#/design/flow` — from the same
> registry the board draws, with small step cards in plain DOM/SVG. No canvas, no live frames, no
> React Flow.

**What to build:** every task a reviewer actually walks, as a numbered row of step cards, generated from
`src/scenes/` — one section per task, each section a small diagram. The register of tasks is editorial
(hand-written in `src/scenes/tasks.ts`); everything else is derived, and the derivation is what keeps it
honest:

- **`TASKS`** — 6 tasks of 3–8 steps, each step a real registry scene id. A step's `via` says how you
  arrived: omitted means *a declared registry link* (and then the connector's label is the link's own
  label, never typed twice); `{ kind: 'nav', label }` names a menu/route hop that is a real control in
  the app ("Sign out").
- **`validateTasks(tasks, scenes)`** — pure, `node --test`-covered: every step exists; every adjacent
  pair is either a declared link or a declared nav hop; a nav hop with a real link behind it is an
  error; ids unique; 3–8 steps. The page renders any problem as a loud block, so a drifted task is
  visible rather than quietly wrong.
- **`computeFlows(tasks, scenes)`** — pure layout, no React: cards left to right, four per row with a
  wrap chip, connectors at card edges, and the connector's `action` (`opens`/`tab`/`nav`) for styling.
- **`#/design/flow`** — inside `AppShell` (it is a document, not a canvas of the shell): one section per
  task, hover a card to light its connectors and dim the other tasks, and each card links out — *On the
  board* (`?scene=<id>#/design/board`) and *Open live* (the scene's exact forced state, the same URL
  builder the board's frame chrome uses).

**Blocked by:** 10 (scene scope), 04 (registry) — both landed.

**Status:** ready-for-human

- [ ] `TASKS` covers sign-in/first run, create a Project, the Project tabs, a partial Compose, a Volume
      handle, and sign-out; every task has a one-sentence `summary` that names the question it answers
- [ ] `validateTasks(TASKS, SCENES)` is empty, and the test proves each problem kind is caught
      (unknown scene, missing link, a nav hop where a link exists, duplicates, a 2-step task)
- [ ] `computeFlows` is render-free and covered by `node --test` — four cards per row, no overlap,
      wrap connectors marked, every connector's coordinates on card edges
- [ ] A link connector's label is byte-identical to the registry link's label — asserted, not eyeballed
- [ ] `#/design/flow` renders inside the shell, is reachable from the sider's Design system group, and
      each card carries its scene id and a working *On the board* and *Open live* link
- [ ] The page imports neither `@xyflow/react` nor the storyboard engine
- [ ] `node --test scripts/flow.test.mjs` and `pnpm --dir web-design typecheck` green

- 2026-09-27 — **Landed and accepted.** Commits `b674261` (model) and `8e32045` (page). `TASKS` is
  six tasks / 25 steps; `validateTasks(TASKS, SCENES)` is `[]` and each problem kind has a failing
  fixture in `scripts/flow.test.mjs` (16 tests). The page was checked over the offline single-file
  build through headless Chrome: the DOM carries 6 `data-flow-task`, 25 `data-flow-card`, 17 line
  connectors and 2 wrap chips (19 hops total), a card's *Board* link reads
  `?scene=projects-cards#/design/board`, and a *Live* link carries its scene's exact state
  (`?ui=auth.setupStep%3D1&after=toast%3Aauth.adminCreated#/setup`); screenshot at 1600×1000 and
  1600×3000 reviewed. `scripts/screenshot.sh` gained `task-flows`. Full `node --test` suite: 61 pass,
  1 skipped (the env-gated registry assertion); `pnpm typecheck` green.
