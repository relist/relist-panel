# 04 — The scene registry: every scene, in menu order, with captions

**What to build:** `web-design/src/scenes/registry.ts` — the catalogue of scenes, the ordering, and the
navigation the flow layer will draw. This is the plan's second half's source of truth: nothing is
captured (05) or laid out (06) that is not declared here.

```ts
type Scene = {
  id: string;                    // stable slug: 'compose-logs-fatal'
  title: string;                 // 'Compose — Logs drawer'
  group: SceneGroup;             // menu group: the column the frame lands in
  kind: 'screen' | 'overlay' | 'feedback';
  device: 'desktop' | 'mobile';  // viewport, the rail it lands in, and the board's device filter
  hash: string;                  // a route built through app/router.ts's `paths`
  ui?: Record<string, unknown>;  // forced state, keys from app/uiKeys.ts
  after?: string;                // a named transition adapter (02)
  viewport: { width: number; height: number };  // desktop 1600×1000, mobile 390×844
  caption: string;               // one line: what to look at, and why it is this way
  menuPath: string[];            // ['Projects', 'my-saas', 'production', 'web'] — order + flow
};

type SceneLink = { kind: 'nav' | 'opens'; from: { scene: string; at?: [number, number] }; to: string };
```

The links are declared here too, not in the flow layer: a menu item's destination, a tab's destination,
and a trigger's `opens` edge are all *data about the scene*, and declaring them beside the scene is what
keeps 06 a pure renderer. `at` is a fraction of the frame box (`[0.97, 0.04]` for a header's ⋯ button),
which is how a trigger is expressible without pixel-picking.

Start from the spec's inventory (~95–105 desktop frames, about a third overlays) and revise it against
the mock now that 02 has made every state reachable, then add the mobile half: **~20–25 `device: 'mobile'`
scenes at 390×844**, one per screen for its primary state plus the states where mobile changes the answer
(the nav drawer, a stacked table, a full-height dialog, the file list, a long log line, the charts) —
designed in 03, and not one per state (see *Mobile: the second viewport*). Every mobile scene names the
desktop scene it is the second viewport of, and every screen worth a phone frame gets one.

Two entries the spec flags as unobtainable have to be resolved here rather than faked:
`ComposeVolumesTab`'s disabled-Open state and `StartHandleDialog`'s absent-path refusal. Either add the
fixture (a small change in `volumeMocks.ts`) or leave them out and note it in Comments — the board must
not draw a frame the app cannot produce.

**Blocked by:** 03

**Status:** needs-triage

- [ ] **Review gate: the scene list is agreed as a list before anything is captured.** The ticket's
      deliverable is the list plus its captions — the review happens before 04 runs, and a scene added
      later is a registry edit, not a re-plan
- [ ] Every scene id is unique, kebab-case, and stable (feedback quotes it, and it is the still's filename)
- [ ] Every `hash` is built through `paths` from `app/router.ts`, so a route rename moves the registry
      with it, and every scene resolves to the route kind it claims (no `not-found` by accident)
- [ ] Every `ui`/`after` name is declared by some screen — asserted mechanically, not by eye
- [ ] Group order is the menu's order, and `menuPath` places every screen scene under the menu item and
      tab it is actually reached from
- [ ] `kind` is honest: a `screen` renders the shell, an `overlay` is an overlay on a screen, `feedback`
      is a state with no home screen
- [ ] Captions say what to check and why the state is the way it is — one line, and the reason the frame
      exists. A caption that only restates the title is a bug in the caption
- [ ] Overlays declare `opens` links to the screen they belong to, and every `nav` link's `from`/`to` are
      real scene ids
- [ ] `node --test scripts/registry.test.mjs` (stdlib runner, no new dependency) asserts the invariants
      above, so 05 can reject a scene that points at nothing
- [ ] Every `device: 'mobile'` scene declares 390×844 and pairs with a desktop scene; a mobile scene whose
      screen has no desktop frame (or vice versa, for a screen that got a phone frame) is a test failure,
      not a judgement call
- [ ] No scene declares a dark variant: the theme switch reviews dark live (see *Dark mode is a switch*),
      so a `dark` field reappearing here is a mistake
- [ ] The list is reviewable at a glance — it is read, not run — because it is the gate 05 captures from
