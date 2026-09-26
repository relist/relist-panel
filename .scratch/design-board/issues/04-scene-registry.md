# 04 — The scene registry: every scene, in menu order, with captions

> **Architecture update — 2026-09-27 (spec.md).** The board renders frames **live**, so every
> "captured" below reads "rendered" and the still filename reads the frame id. The registry type,
> ordering, links and invariants stand as written. **Blocked-by change:** desktop scenes need only
> 02; non-desktop scenes stay gated on 03 — declare `viewports` honestly now, render them after 03.
> The width matrix question is gone: a live frame renders any declared width with no second capture.

**What to build:** `web-design/src/scenes/registry.ts` — the catalogue of scenes, the ordering, and the
navigation the flow layer will draw. This is the plan's second half's source of truth: nothing is
captured (05) or laid out (06) that is not declared here.

```ts
type Scene = {
  id: string;                    // stable slug: 'compose-logs-fatal'
  title: string;                 // 'Compose — Logs drawer'
  group: SceneGroup;             // menu group: the column the frame lands in
  kind: 'screen' | 'overlay' | 'feedback';
  viewports: ViewportKey[];      // 'desktop' | 'tablet' | 'mobile' — which widths this scene is captured at
  hash: string;                  // a route built through app/router.ts's `paths`
  ui?: Record<string, unknown>;  // forced state, keys from app/uiKeys.ts
  after?: string;                // a named transition adapter (02)
  caption: string;               // one line: what to look at, and why it is this way
  menuPath: string[];            // ['Projects', 'my-saas', 'production', 'web'] — order + flow
};

type SceneLink = { kind: 'nav' | 'opens'; from: { scene: string; at?: [number, number] }; to: string };
```

The links are declared here too, not in the flow layer: a menu item's destination, a tab's destination,
and a trigger's `opens` edge are all *data about the scene*, and declaring them beside the scene is what
keeps 07 a pure renderer. `at` is a fraction of the frame box (`[0.97, 0.04]` for a header's ⋯ button),
which is how a trigger is expressible without pixel-picking.

`ViewportKey` is `'desktop' | 'tablet' | 'mobile'`, with the sizes from the spec's `VIEWPORTS`
(1600×1000, 834×1112, 390×844) declared once and imported by the board — a scene that listed a width
whose size lived only in the registry would be two sources of truth for one number.

Start from the spec's inventory (~95–105 desktop frames, about a third overlays) and revise it against
the mock now that 02 has made every state reachable, then add the other viewports: **~20–25
`viewports: ['mobile']` scenes at 390×844** (one per screen for its primary state, plus the states where
mobile changes the answer: the nav drawer, a stacked table, a full-height dialog, the file list, a long
log line, the charts) and **~10 `viewports: ['tablet']` scenes at 834×1112** (the screens whose layout
genuinely differs there — Projects, Overview's stat rows, Variables, Monitoring, Compose, the Explorer).
Designed in 03; every non-desktop scene names the screen it is the second viewport of, and a screen worth
a phone frame gets one.

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
- [ ] Every `viewports` list is explicit and honest: a scene captured at one width says so, and a scene
      captured at several lists them all. Nothing implies a variant that was not designed in 03
- [ ] Non-desktop scenes pair with a desktop scene of the same screen, and the pairing is asserted — a
      tablet or phone frame with no screen behind it is a test failure, not a judgement call
- [ ] No scene declares a dark variant: the theme switch reviews dark live (see *Dark mode is a switch*),
      so a `dark` field reappearing here is a mistake
- [ ] The list is reviewable at a glance — it is read, not run — because it is the gate 05 captures from
- [ ] The tablet set is chosen, not defaulted: ~10 scenes, each because the 834px layout differs from both
      desktop and phone. A tablet frame that just looks like a wider phone is a wasted capture
