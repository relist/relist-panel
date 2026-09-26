# 10 — Per-frame scene scope: one document, N isolated frames

**What to build:** `web-design/src/scenes/scope.tsx` — the `SceneScopeProvider` that lets the board
(ticket 06) mount N live frames in **one** document without them leaking into each other or into the
standalone app. Ticket 01's harness is module-level and read-once, which is right for a page that
hosts one scene; the board hosts a hundred. This ticket converts the harness from a singleton into a
scope without changing standalone behaviour.

The provider wraps one frame and supplies, scoped to it:

- **Route.** `useRoute` / `useActivePath` / `navigate` (`app/router.ts`) read and write a
  context-provided route when a scope is present, and the window hash exactly as today when not.
  The hash router's exported signatures stay identical. The one mount-time `navigate` there is
  (ComposePage's stale-link redirect, `ComposePage.tsx:133`) lands in the frame's own route state —
  absorbed, invisible.
- **Forced state.** The scope's override map (from its `Scene`) takes precedence over ticket 01's
  module map; the module map stays the standalone `?ui=` path. `useUiState` consults the scope
  first.
- **Clock.** A scoped clock (the `?clock=` value as context) feeds `useNow` and the store seeds;
  `scene.ts`'s `globalThis.Date` patch stays for standalone use only.
- **Feedback.** A nested `<AntApp>`, so every `App.useApp()` toast inside the frame is frame-local
  with zero call-site changes. `ConfigProvider getPopupContainer` → the frame box, so
  Dropdown/Select/Tooltip/Popconfirm portals land inside it.
- **Modal/Drawer containers.** `useSceneContainer()` returns the frame box inside a scope and
  `undefined` outside; the ~16 Modal/Drawer sites (ticket 02's audit §2) pass it as `getContainer`.
- **`--frame-vh`.** The frame wrapper sets `--frame-vh` from the scene's declared height, and the
  handful of `vh`-based declarations switch to it: `theme.ts` `editorMinHeight`/`volumeEditorHeight`,
  `AppShell`'s sider calc, `FullscreenEditor`'s `100vw`/`100vh` (→ `%`), the logs drawer's
  `calc(100vh - 220px)`. A page with no scope renders exactly as today (`var(--frame-vh, 1vh)`).
- **Monaco isolation.** A scoped model-path prefix (`useSceneModelPrefix()`) applied in
  `YamlEditor`/`TextFileEditor`, so two frames of one screen never share a model; within one frame,
  ComposePage and FullscreenEditor still share theirs deliberately (`keepCurrentModel`).
- **Readiness.** `data-scene-ready` moves onto the frame's own element when scoped
  (`app/sceneReady.ts` keeps the `<html>` behaviour standalone), so ticket 06 can assert per frame.
- **Error boundary.** A frame that throws renders a plain red card naming its scene id and the
  error — never a blank board, never a crash of the canvas.
- **Static.** The scope's outer wrapper sets `pointer-events: none` (the board re-enables it for its
  own chrome). Frames do not navigate; *Open live* is the interactive path.

**Blocked by:** 01 · **Blocks:** 06 · **Status:** ready-for-agent

- [ ] Standalone behaviour is unchanged: with no `?ui=`/`?scene=`, every screen renders and behaves
      exactly as before, and `pnpm --dir web-design typecheck` is green
- [ ] Two scopes mounted side by side (one dev-only harness page, removed by 06 or kept as the
      engine's fixture) prove isolation: forcing a route/navigation in one does not move the other;
      a toast fired in one does not appear in the other; the two frames' Monaco models have
      distinct paths; one frame throwing leaves the other rendered
- [ ] `Modal`/`Drawer`/`Dropdown`/`Select`/`Popconfirm` portals land inside their frame's box
      (asserted in the DOM: the overlay's offsetParent is inside the frame wrapper)
- [ ] `--frame-vh` resolves against the declared frame height, and a `clamp(640px, …, 1000px)`
      editor inside an 800-tall and a 1200-tall frame comes out different, on purpose
- [ ] `after:` adapters fire once per frame in dev despite `StrictMode` double-effects (idempotent
      or keyed), and scoped readiness reflects the frame, not the page
- [ ] No behaviour is added beyond scoping; anything that looks wrong while converting is recorded
      in Comments, not fixed here
