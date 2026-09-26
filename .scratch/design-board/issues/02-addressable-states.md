# 02 — Addressable states: the swap, and the two fictions

**What to build:** The `useUiState` swap across the mock — the ≈55 of the mock's 80 `useState` sites
that gate something visible, per the inventory in the spec (`### Forcing state without a click`) — plus
the two families of state that need a small fiction, so the registry (03) is written against states
that can actually be forced. Mechanically repetitive, conceptually empty: the point is coverage, so the
counts in the spec's table are the acceptance list, not a sample.

Ordered smallest-first so the pattern is agreed before the largest file is touched: `AppShell` (2),
`OverviewPage` (1), `MonitoringPage` (3), `ProjectsPage` (6), `ProjectPage` (5), `VariablesPanel` (6),
`ComposePage` (8), `ComposeVolumesTab` (3), `StartHandleDialog` (3), `ServiceMonitor` (4),
`StatesGalleryPage` (the scene host that survives 08) and the remaining files (~20 sites), then
`VolumeExplorerPage` (13, the largest) last. `ScreensGalleryPage` is not converted at all: 08 deletes
the page, so touching its four sites would be work in a file on its way out.

Two families get a fiction, and both are declared rather than painted:

- **Toasts.** `message.success`'s 3 s timeout means a scene can never be captured mid-toast. Scenes that
  want one call it on mount with `duration: 0` through an `after: 'toast:…'` adapter registered here;
  the copy is the app's own, so the frame shows what the app produces.
- **Post-action states.** Validation errors only exist after a submit. `after: 'submit'` (already parsed
  in 01) performs the real store/UI transition on mount — the modal's own submit handler, not a fake
  error object — and readiness waits for it to settle before `data-scene-ready` goes up.

Both live behind a small registry of named adapters in `web-design/src/app/sceneAdapters.ts`, keyed by
the `after` string, so a scene names an outcome and the adapter owns how to reach it.

**Blocked by:** 01

**Status:** needs-triage

- [ ] The spec's per-file table is filled in per site — file, line, key, and the state it forces — and
      attached to this ticket's Comments when work starts; anything excluded is excluded by the stated
      rule (derived values, `useRef`/`Form.useForm`, router state), not by convenience
- [ ] Every converted key is registered in `app/uiKeys.ts`, and forcing it from `?ui=` produces the
      state it names on a cold load
- [ ] Controlled antd overlays are forced with `open` where the component supports it (`Dropdown`,
      `Popconfirm`, `Select`, `Tooltip`, `Modal`, `Drawer`); nothing reaches into an overlay's internals
- [ ] The `after` adapters cover the toast and post-action scenes the spec's inventory lists, and a
      scene that names an unknown `after` value fails loudly in dev
- [ ] Screens are unchanged for a reviewer who never passes a query string: no visible behaviour moves,
      and `pnpm --dir web-design typecheck` is green
- [ ] Per screen, the CDP render check asserts the forced state — the pattern ticket 01 of `volume-files`
      used: drive the built bundle, assert on the DOM, screenshot the state. The states asserted are
      exactly the scene's `ui` keys, so 03 inherits a validated catalogue
- [ ] No behaviour is added to the mock beyond addressability: the swap must not fix, tidy or prune a
      screen. Anything that looks wrong while converting gets recorded in Comments, not fixed here
