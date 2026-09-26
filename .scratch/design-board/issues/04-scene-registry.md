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

## Comments

- 2026-09-26 — **Implemented.** `web-design/src/scenes/` now holds the registry:
  `types.ts` (the `Scene` type below), `groups.ts` (the ordered group list),
  nine `groups/<id>.ts` files (one per group, so the list is reviewable in
  pieces), and `registry.ts` (`SCENES` + `sceneById`). The invariants live in
  `web-design/scripts/registry.test.mjs` (`node --test`, stdlib runner).
  **105 scenes** — 56 screens, 38 overlays (≈ a third), 11 feedback — with
  `viewports: ['desktop']` on every one until 03. Per group: overview 7 (incl.
  the 3 shell overlays), projects 9, monitoring 4, project 21, compose 21,
  volume-explorer 24 (incl. the 5 Start Handle dialog scenes), design-system 1,
  auth 7 (incl. `not-found`), feedback 11. The review gate above was taken as
  satisfied by the coordinator's brief (the coverage list came from it); edits
  from review are registry edits, per the gate's own rule.

- 2026-09-26 — **Two type additions, as instructed, plus one more to record.**
  (a) `Scene.size: { width: number; height: number }` — the engine's
  `BoardScene` needs a natural size: `width` is the primary viewport's width
  (`VIEWPORT_SIZES.desktop.width`, declared once in `scenes/types.ts` and
  cross-checked by the test, so the number is never typed twice), `height` is a
  content estimate that only sizes the layout — live frames self-measure later.
  (b) `ui` keys stay **plain strings**: the registry deliberately does not
  import `SCREEN_UI_KEYS` and type against it (a compile-time coupling would
  force the registry and ticket 02's batches to land serially); the names are
  asserted mechanically by the test instead. (c) `SceneLink.kind` gains
  `'tab'`: the ticket's prose already wants a tab's destination declared beside
  the scene ("a menu item's destination, a tab's destination, and a trigger's
  `opens` edge are all data about the scene") but the snippet's union only had
  `nav | opens`. The board page maps `opens` → `BoardOpensLink` (inverted onto
  the parent), `tab` → `BoardTabLink`, and derives `nav` from `menuPath`.

- 2026-09-26 — **Both flagged unobtainable states resolved by fixture, not
  faked.** `data/volumeMocks.ts` gains one Volume on Compose 1:
  `bind('./retired', '/app/retired', ['api'], false, 'absent')` — declared in
  the YAML, stat-ed, not there. `StartHandleDialog`'s absent-path refusal
  (`start-handle-absent`, OK disabled) and `ComposeVolumesTab`'s disabled-Open
  are now real reachable states; the volumes-live scene shows the disabled Open
  row among web's six Volumes. The file's doc comment is updated to say six
  shapes. This was the only change outside `src/scenes/**` +
  `scripts/registry.test.mjs`.

- 2026-09-26 — **Registry shape, for whoever maintains it.** Each group file
  exports its scenes as a **function of `paths`** (`scenes(paths)`) and has
  zero runtime imports (type-only imports only). That is what lets the test
  rebuild `SCENES` under plain Node — extensionless runtime imports do not
  resolve there, so `registry.ts` itself cannot be imported by the test —
  while every `hash` is still built through `app/router.ts` at assembly time.
  `groups.ts` owns the order; `registry.ts` only wires (`GROUPS.flatMap` over a
  complete `Record<SceneGroupId, …>` map the compiler checks). The test
  rebuilds the array the same way and asserts the two cannot drift.

- 2026-09-26 — **Test results.** `node --test scripts/registry.test.mjs`: 16
  pass, 1 skipped, 0 fail; together with the existing `scene.test.mjs` and
  `storyboard.test.mjs`: 45 pass, 1 skip, 0 fail. `pnpm --dir web-design
  typecheck` green (registry files included; the volumeMocks edit typechecks).
  The skipped test is the ticket's "every `ui`/`after` name is declared by some
  screen", gated behind `REGISTRY_ASSERT_KEYS=1` because batch 1 of ticket 02
  was landing in parallel while the registry was written. Run
  `REGISTRY_ASSERT_KEYS=1 node --test scripts/registry.test.mjs` for the live
  list. At writing it names, by owner: **batch 1 leftovers** — ui key
  `shell.accountMenu` (audit §2's AppShell account dropdown, not yet declared),
  adapters `monitoring:loading` and `monitor:loading` (D4's loading adapters
  for `MonitoringPage` and `ServiceMonitor`); **everything else is batch 2**
  (`projects.*`, `project.*`, `variables.*` keys; `object:`/`fixture:`/
  `submit:projects.new.empty` adapters) **or batch 3** (`compose.*`, `volume.*`
  keys; `volumes:loading`, `volume:loading`, `object:volume.*`, `seed:volume.*`
  adapters). The scan is repo-wide (`registerSceneAdapter(` across `src/`),
  because batches register from their own screen files.

- 2026-09-26 — **The adapter-name contract** (the names this registry names,
  following batch 1's grammar — `toast:` per audit §3a verbatim,
  `submit:<screen>.<surface>.<variant>` per §3b, `object:<uiKey>` per D1 as
  batch 1's `object:volumes.starting` established, `fixture:` for the §5.10
  named-document states, `seed:` for store-shape seeds, `<area>:loading` for
  D4's loading holds). Already registered: `submit:auth.signin.empty`,
  `submit:auth.setup.mismatchedConfirm`, `toast:auth.adminCreated`,
  `toast:states.deployed`, `toast:states.deployNotification`. To register —
  batch 1: `seed:overview.allClear` (seeds every Compose fully running, for
  `overview-all-clear`), `monitoring:loading`, `monitor:loading`. Batch 2:
  `submit:projects.new.empty`, `object:projects.editing`,
  `object:projects.deleting` (also types the name),
  `object:project.deletingCompose` (also types), `object:variables.editing`,
  `fixture:project.composeYAML` (STARTER_YAML, §5.10),
  `fixture:variables.bulkText`, `fixture:variables.bulkDuplicate`,
  `seed:projects.empty`, `seed:project.zeroEnvs`. Batch 3:
  `fixture:compose.draft`, `volumes:loading`, `volume:loading`,
  `object:volume.editing`, `object:volume.editorTooLarge`,
  `seed:volume.handleRo`, `seed:volume.uploadTooLarge`,
  `seed:volume.uploadNoSpace`. Until an adapter lands, its frame renders the
  plain screen — visible in review, never silently wrong (the gated test names
  it).

- 2026-09-26 — **New `uiKeys.ts` declarations the registry needs** (beyond
  batch 1's list): key families per D2 — `projects.rowMenu.*`,
  `project.rowMenu.*`, `project.deleteEnv.*`, `variables.deletePopconfirm.*`,
  `volume.rowMenu.*`, `volume.deletePopconfirm.*` — and the batch-2/3 screen
  keys the scenes force: `projects.view`, `projects.query`, `projects.modalOpen`,
  `project.environmentModalOpen`, `project.composeModalOpen`,
  `project.deleteProjectOpen`, `variables.revealed.*`, `variables.query`,
  `variables.modalOpen`, `variables.bulkOpen`, `compose.moreOpen`,
  `compose.deleteOpen`, `compose.logsOpen`, `compose.logsLoading`,
  `compose.fullscreenOpen`, `volume.startOpen`, `volume.expiredHere`,
  `volume.uploadError`, `volume.newEntry`, `volume.downloadOpen`. (Audit §1 is
  the source for every name; the families are the audit's own §5.5 rows under
  D2's rule.)

- 2026-09-26 — **Deviations from the spec's inventory, and why.**
  (1) *Running-stack write warning* is not a separate frame: the seeded handle
  is rw on a running Compose, so the banner is already on
  `volume-explorer-root` — a second scene would be pixel-identical. The
  Explorer scene list carries 19 screens, not the spec's 20.
  (2) The mission's Project list names both "no-envs" and "zero-envs"; they are
  the same state (`environments.length === 0`), declared once as
  `project-zero-envs`.
  (3) `not-found`'s hash is built *from* `paths` by concatenation
  (`` `${paths.projects}/nope` ``) because no `paths` entry produces the
  router's catch-all; the test pins both the derivation and that
  `not-found` is the only scene parsing to `kind: 'not-found'`.
  (4) `states.section` feedback scenes are `kind: 'feedback'` (no home screen;
  the gallery is a review host) — so `feedback-destructive` shows the gallery's
  own DangerModal without an `opens` edge; the test's overlay rule does not
  apply to `feedback` kind.

- 2026-09-26 — **Open questions for the coordinator.**
  (a) `overview-all-clear`, `projects-empty`, `project-zero-envs`,
  `volume-explorer-readonly` have no forcing mechanism today: attention and the
  projects list are store-derived, `seedHandle` hardcodes `mode: 'rw'`, and
  `SceneOverrides` carries only activity/handles/speed. Each names an adapter
  (contract above); if the batch owners instead prefer a store lever (e.g. a
  `projects`/`handles-mode` override in `SceneOverrides`), the registry entries
  need only their `after` swapped — the scenes themselves stand.
  (b) The three loading scenes (`monitoring-loading`, `compose-monitor-loading`,
  `volume-explorer-loading`, plus `compose-volumes-loading`) depend on D4's
  "the loading scene is an `after:` adapter" — worth confirming the batch
  owners agree a *held* skeleton is reachable that way, since the underlying
  state is excluded as data.
  (c) The audit's §5.6 finding stands unfixed by design: the Explorer's
  delete-entry `Popconfirm` has no `onConfirm`, so
  `volume-explorer-delete-popconfirm` renders an honest Popconfirm whose
  confirm is a no-op; its caption says so.
