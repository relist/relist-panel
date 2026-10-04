# 01 — The scene harness: `?ui=`, `?clock=`, `?scene=`

**What to build:** One module that a scene URL goes through, imported **first** by
`web-design/src/main.tsx` so it runs before the rest of the module graph evaluates:

- `web-design/src/app/scene.ts` — parses `window.location.search` **once** into
  `{ clock, scene, ui }`: `?ui=compose.logsOpen=1&ui=compose.tab=logs`, `?clock=2026-09-26T09:00:00Z`,
  `?scene=<id>`. Everything else re-exports from here (`useUiState`, `sceneOverrides()`, `sceneId()`,
  and the `after` parameter below), so there is one parse and one place where load order matters.
- **The frozen clock, patched rather than plumbed.** When `?clock=` is present, `Date.now` and
  `new Date()`'s no-argument form return the fixed instant for the life of the page. No fixture and no
  screen is edited: the six call sites in the spec's table (`useNow`, the store's seeded handle,
  `metricSamples`, `volumeMocks`'s `mtime`, `GuidelinesPage`'s module-scope stamps, the log fixture)
  already read those two.
- `useUiState(key, initial)` (`web-design/src/app/uiState.ts`) — `useState`, with the override map
  winning over `initial`. Keys are declared in one place, `web-design/src/app/uiKeys.ts`, so a URL key
  that no screen reads is a dev-mode warning rather than a silent no-op. Ticket 03 asserts every scene's
  `ui` keys are in that list.
- `SceneOverrides` — a prop on `PanelProvider` (`data/store.tsx:103`) seeding store-level state (which
  Volume Handle is open, the mock latency, the activity log) for the mount's lifetime. No override means
  the seed, which is what "back to normal" means for storage state — the screen's own `useState` calls
  are the seeds and stay untouched.
- `?after=` is parsed here and left inert; ticket 02 supplies the per-screen adapters it dispatches to.
- **Readiness**: `<html>` carries `data-scene-ready` once store `pending` is empty and
  `document.fonts.ready` has resolved, with the id from `?scene=` echoed back — the capture pipeline's
  own assertion that it photographed the scene it asked for. Monaco-idle and the animation-freeze
  injection belong to 04.

**Blocked by:** — (can start immediately)

**Status:** ready-for-human

- [ ] `main.tsx`'s first import is the harness, with a comment saying why: `volumeMocks.ts` calls
      `mtime()` at module scope through `file()`/`dir()`/`symlink()`, so the fixture timestamps are
      computed while the graph is still evaluating — a clock installed anywhere later is too late
- [ ] `?clock=<ISO>` freezes `Date.now()` and `new Date()` for the whole page and changes nothing else
- [ ] With no `?clock=`, the mock behaves exactly as it does today — one code path, not two
- [ ] All six clock call sites are fixed by the patch, verified by eye on the Volume Explorer's
      mtimes, the Overview banner's countdown, the Monitor chart's axis and the log timestamps
- [ ] `useUiState('x', a)` returns `a` when `?ui=` has no `x` and the override when it does; values are
      coerced from the URL's strings (`1`/`0`/`true`/`false`, else the string)
- [ ] An undeclared `ui` key warns once in dev, with the key in the message
- [ ] `SceneOverrides` seeds the store without touching any screen's own `useState`
- [ ] `data-scene-ready` appears with the `?scene=` id, and drops while the store is pending
- [ ] Acceptance — determinism: the same route loaded twice, a minute apart on the wall clock with the
      same `?clock=`, renders identical relative-time text, identical seeded-handle values, identical
      chart axis labels, and `Date.now()` in the page returns the frozen instant.
      (`?clock=` + `TZ=UTC` is the pair; the byte-identical-WebP version of this check is 04's.)
- [ ] Acceptance — isolation: `pnpm --dir web-design typecheck` is green and every route renders
      unchanged when the query string is empty

## Comments

- 2026-09-26 — Claimed. `Status:` moved to `ready-for-agent`; implementation runs on branch
  `feat-design-board`. Owner: a dedicated implementation sub-agent; acceptance verified afterwards by a
  second, independent sub-agent against the checklist above (the coordinator closes the ticket only after
  that verification).
- 2026-09-26 — Coordination, so two agents do not collide:
  - The per-site `useState` inventory the spec's *Forcing state without a click* calls for is being
    produced by a separate read-only audit and lands under **ticket 02's** Comments, not here.
  - The `useUiState` swap across the mock's screens is **ticket 02's** work and is explicitly out of this
    ticket's scope; so are the `after:` adapters, Monaco-idle readiness and the animation-freeze
    injection (the pipeline ticket's).
  - This ticket delivers: `app/scene.ts` (first import of `main.tsx`), `app/uiState.ts`, `app/uiKeys.ts`,
    the readiness signal, `SceneOverrides` on `PanelProvider`, `?after=` parsed and left inert, and
    `scripts/scene.test.mjs`.
- 2026-09-26 — **Built and verified.** Commits on `feat-design-board`: `09fb0cd` (parse, coercion,
  declared keys, tests), `627dfa5` (frozen clock, store overrides, readiness, `main.tsx` wiring),
  `80bd093` (code-review fixups). This Comments section is the `docs:` commit that carries it. Typecheck
  and the test file were green at every one of them.

  **What landed, file by file**

  - `web-design/src/app/uiState.ts` — the DOM-free half of the harness: `parseSceneUrl`,
    `coerceUiValue`, the module-level override map, and `useUiState` (`useState` plus "a value the URL
    forced wins over `initial`", read when the state is created). Import-light on purpose — React and
    nothing else of this app — because it is the module `scripts/scene.test.mjs` imports. That is the
    one deviation from the ticket's file map, and it is forced: under `node --test` a **relative**
    TypeScript import does not resolve (`ERR_MODULE_NOT_FOUND` for both `./a` and `./a.js`; only a full
    `./a.ts` specifier resolves, and TypeScript rejects that without `allowImportingTsExtensions`, a
    `tsconfig.json` edit outside this ticket's scope). So the pure parse lives in `uiState.ts` and
    `scene.ts` — which imports relatives freely, being browser-only — is the bootstrap that calls it
    once and re-exports what the app needs, as the ticket asks.
  - `web-design/src/app/uiKeys.ts` — the declared keys: `SCREEN_UI_KEYS` (empty until 02 registers the
    swapped sites), the three `store.*` keys as a const object so `sceneOverrides()` reads them by name
    and the declaration cannot drift from the reads, and `createUndeclaredUiKeyWarner` — once per key,
    the key named in the message, the `warn` sink injected so the tests can drive it without a dev
    server.
  - `web-design/src/app/scene.ts` — `main.tsx`'s first import: one parse of `window.location.search`
    into `{ clock, scene, ui, after }`, the `Date` patch, `sceneId()`, `sceneAfter()` (parsed, inert),
    `sceneOverrides()`, and the re-exports (`useUiState`, the `SceneUrl`/`UiValue` types). Guarded with
    `typeof window !== 'undefined'`, so importing it outside a browser is inert rather than fatal.
  - `web-design/src/app/sceneReady.ts` — `SceneReadiness` (renders nothing): publishes
    `data-scene-ready` on `<html>` carrying the `?scene=` id once the store's `pending` is empty **and**
    `document.fonts.ready` has resolved, and removes the attribute otherwise. Monaco-idle and the
    animation-freeze injection are deliberately not here (04/05 extend this signal).
  - `web-design/src/data/store.tsx` — the `SceneOverrides` prop on `PanelProvider`, and `pending`
    exposed on the panel value (two lines) so readiness has something to read. The store's own
    `useState` calls are still the seeds; no state moved anywhere. The seeded handle's literal became a
    small `seedHandle`/`seedHandles` pair so a scene can name volume ids — the default path is
    unchanged, which the pre-change comparison below proves byte for byte.
  - `web-design/src/main.tsx` — `import './app/scene'` first, with the comment saying why the order is
    load-bearing, `<PanelProvider overrides={sceneOverrides()}>`, and `<SceneReadiness />`.
  - `web-design/scripts/scene.test.mjs` — `node:test` only: no framework, no new dependency, no
    `package.json` change.

  **Decisions the ticket left open (settled here, flagged for the coordinator)**

  1. **How a scene forces *store* state.** The registry's only field for forced state is
     `ui: Record<string, unknown>` ("keyed as useUiState keys"), so the three storage-level things the
     spec names are reached through `?ui=` under declared store keys: `store.speed=instant|normal|slow`,
     `store.handles=<volumeId>[,<volumeId>]` or `none` (the handle's *shape* stays in `store.tsx`;
     `SceneOverrides.handles` is a list of volume ids, not a handle object), and `store.activity=empty`.
     `DECLARED_UI_KEYS` = screen keys + store keys, so ticket 03's "every scene key is declared" check
     covers them and no dev warning fires for them.
  2. **Coercion.** `1`/`true` → `true`, `0`/`false` → `false`, everything else the string it was —
     literally the ticket's words. A *numeric* scene key would therefore arrive as `'2'`, which is a
     screen-key problem for 02/04 rather than a rule to guess at here.
  3. **`?ui=key` with no `=`** is `true` (the URL has no other way to spell "on"), while `?ui=key=` is
     the empty string. Both are covered by tests.
  4. **`data-scene-ready` is published only when `?scene=` is present** — with an empty query string
     there is no attribute at all, which is what keeps "renders unchanged" literally true.
  5. **`pending` on the panel value.** Readiness needs to see the store's in-flight map; exposing it
     (two lines, already a memo dependency) was chosen over having `data/` import `app/` to report into
     it. The store's state itself is untouched.
  6. **`scene.test.mjs` also covers the hook**, through React's own server renderer (`react-dom/server`,
     already a dependency): a forced key wins, an unforced one keeps its seed. Without a screen swapped
     to `useUiState` yet (that is 02), that was the only real seam for the ticket's fifth box.

  **Verification actually run (2026-09-26; Chrome 152.0.7977.82, Node v24.20.0, `TZ=UTC`)**

  - `pnpm --dir web-design typecheck` → `$ tsc --noEmit`, no output, exit 0.
  - `node --test web-design/scripts/scene.test.mjs` → `tests 13  pass 13  fail 0` (four suites: the URL
    map, coercion, the hook, undeclared-key detection with its once-per-key warner).
  - Determinism, end to end: `pnpm --dir web-design build:single`, `python3 -m http.server` over
    `dist-single`, `google-chrome --headless=new --remote-debugging-port=9223` with `TZ=UTC`, driven over
    CDP by a throwaway `/tmp/scene-check.mjs` (global `WebSocket`, no dependency). Five routes — `#/`,
    `#/monitoring`, the Volume Explorer's uploads bind, the Compose Monitor tab and `#/design/guidelines`
    — were loaded **with `?clock=2026-09-26T09:00:00Z`**, again **65 s later**, then with
    `?clock=2026-09-26T21:30:00Z`, with no clock, and with no query string at all. Observed: `Date.now()`
    in the page = `1790413200000` = the frozen instant on every route; and across the 65 s gap,
    **identical** `innerText`, identical `<svg text>` (axis labels), identical `<svg path>` data and an
    identical `Date.now()` on all five routes. With the clock moved 12 h 30 m the Monitoring and Compose
    Monitor axis labels and every chart path change (`08:15 … 09:00` → `20:45 … 21:30`) — the other half
    of the check: the mock follows the clock rather than merely reporting it. With no `?clock=` at all,
    `Date.now()` in the page tracks the real clock (delta −3…−15 ms against the harness's own clock), so
    the patch is inert by default: one code path, not two.
  - The clock in one page, no reload, 70 s apart (`/tmp/scene-ticking.mjs`): frozen — `Date.now()` still
    `1790413200000`, Overview still `closes in 23 min`, activity still `41 minutes ago`; **control, no
    clock** — `Date.now()` advanced 70 008 ms and the countdown fell to `closes in 22 min`. That is
    `useNow`'s two call sites (`app/useNow.ts:11,14`) and the seeded handle's `openedAt`/`startedAt`
    (`data/store.tsx:121,128`) reading the frozen clock, in the only place the difference is observable:
    every fixture is relative to load time, so a *reload* resets it.
  - Readiness: on the loading routes the attribute stayed absent for 11–13 consecutive 60 ms samples
    before appearing as `smoke-monitoring` / `smoke-volume-explorer` / `smoke-compose-monitor`, and it
    carries `?scene=`'s own id on every route. Clicking **Refresh statuses** on Overview removed the
    attribute for ~500 ms (`[null ×5, "drop" ×25]`) and then restored it: it drops while the store is
    pending, not merely when it is late.
  - Isolation: with the clock pinned, every route's `innerText`, `<svg text>` and `<svg path>` are
    identical with and without the harness's own `?scene=`; without a clock the three clock-free routes
    are identical too, and on the two chart routes the only difference is the wall clock ticking between
    the two loads. Against the pre-change tree (`git archive HEAD~2` → a second `dist-single` on `:8124`)
    with **both sides pinned to the same instant** — the old build's instant injected with
    `Page.addScriptToEvaluateOnNewDocument`, since it has no `?clock=` — all five routes come out
    byte-identical in text, SVG text and SVG path data, and so do the five this ticket is not otherwise
    about: `#/design/screens`, `#/design/states`, `#/signin`, `#/setup` and a not-found hash. Empty query
    string: no `data-scene-ready` anywhere, and nothing else changed.
  - Dev-only warning, under `pnpm --dir web-design dev --port 5199`, driven over CDP with console
    capture: `?ui=compose.logsOpen=1&ui=compose.tab=logs&ui=not.a.real.key=42&ui=not.a.real.key=43`
    produced exactly three warnings — one each for the two keys no screen reads yet, and **one** for
    `not.a.real.key` despite it appearing twice — as
    `[scene] ?ui=not.a.real.key is not declared in app/uiKeys.ts — no screen reads it, so it forces nothing.`
    The same page reported `Date.now()` = `1790413200000` and `new Date().toISOString()` =
    `2026-09-26T09:00:00.000Z`, so the patch holds in dev too.
  - `SceneOverrides`, in the built bundle: `?ui=store.speed=slow` made the top strip's "Mock latency"
    control read **Slow**, `?ui=store.handles=none` removed the "1 Volume Handle is still open" banner,
    `?ui=store.activity=empty` removed the seeded activity rows, and with nothing forced the Overview
    `innerText` digest is unchanged (`cd0b50ba54dd`, 2187 chars). No screen file was edited, so no
    screen's own `useState` moved.

  **One spec/README mismatch, for the record.** The clock table's last row ("`data/mocks.ts:641` — the log
  fixture's relative timestamps") does not describe what is there: `composeLogs` (`data/mocks.ts:451`) is
  static text with absolute timestamps and reads no clock, while line 641 is `serviceMetricSamples`'
  `now = Date.now()` default — the Compose Monitor tab, which *was* verified above (its axis labels and
  paths move with the clock). Nothing in the log fixture needed or got a change.

  **Checklist.** All ten boxes are closed. Two of them, restated with their evidence: "values are coerced
  from the URL's strings" is the `coerceUiValue` suite; "`SceneOverrides` seeds the store without touching
  any screen's own `useState`" is the store diff (only `PanelProvider`'s props and the seed pair) plus the
  forced-latency / no-banner / empty-feed observation above. The one thing this ticket could not verify
  the way the ticket words it is `useUiState` at a *screen*: no screen reads a key until 02 does the swap,
  so the hook is covered by the renderer test and the store keys by the DOM check, and 02 inherits the
  seam.

