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

**Status:** needs-triage

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
