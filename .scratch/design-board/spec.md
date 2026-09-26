# Spec — The still board: every screen as a static frame on one canvas

## Problem

`#/design/screens` (the current review surface, built for ticket 01 of `volume-files`) renders 18 live
iframes and asks the reviewer to *click around* to reach the state they want to judge. Three things
are wrong with it:

1. **Most states cannot be seen at all without interacting.** Counting only the overlays, the mock
   has more than twenty `Modal`/`Drawer`/`Popconfirm` sites across 11 files, plus per-screen `useState` that gates
   everything from the Logs drawer (`ComposePage.tsx:103`) to the paste-a-`.env` preview
   (`VariablesPanel.tsx:66`) to "Upload refused" (`VolumeExplorerPage.tsx:110`). The gallery links to
   *routes*, so none of them are reachable from it — they are only reachable by clicking, in the
   running app, once, by whoever happens to be looking.
2. **A reviewer cannot tell what they are looking at.** Frames are numbered 1–18 with no stable id, so
   "frame 12 is wrong" is the most precise feedback the surface can carry.
3. **The layout is a document, not a board.** It reads top-to-bottom like a page and says nothing about
   *navigation* — where a menu item or a tab actually takes you. The user asked for exactly that: the
   vertical menu and the horizontal tabs, drawn as a flow.

## Answer

Yes, all of it is buildable, and none of it needs a new dependency. The two parts that need care:

- **Forcing a state without a click.** Every screen's state has to become addressable. Mechanical, but
  not small, and the count matters more than it looks: the mock holds **80 `useState` sites across 22
  files**, of which the **≈55 that gate something visible** move to a `useUiState` hook — the rest are
  store state, refs and derived values, and stay where they are (see *What converts, and what does
  not*). It is the foundation — the board, the capture pipeline, and shareable state URLs all rest on
  it.
- **Capture determinism.** The mock reads the wall clock in six places, and one of them runs at
  *import* time (`volumeMocks.ts`'s `mtime`, reached through the fixture helpers while the module graph
  is still evaluating), so a clock installed from `main.tsx`'s body is already too late. A frozen clock
  plus a pinned timezone fixes it, and both are load-order decisions rather than features.

Nothing else here is hard. What follows is the detailed plan.

## Decision

Review moves to a **still board**: one pannable, zoomable canvas of static frames — one frame per
screen-state, overlays included — ordered by the menu, with navigation drawn as lines.

Four decisions carry the design:

1. **Frames are captured images, not live iframes.** The scene count is around a hundred, and each
   live frame is a full React root with its own store and its own Monaco instance. The 18-frame
   gallery already weighs what it weighs; a hundred of them is not a review surface, it is a stress
   test. Stills also make the board *static* in the literal sense the user asked for: nothing animates,
   nothing can be clicked by accident, and nothing claims to be live. Whether they also make it
   viewable by someone with no Node and no Chrome depends on what is built and committed — see
   **Shipping the board**, which is a decision in this spec rather than a footnote, because images
   alone do not make a route openable.
2. **A scene is the unit, not a screen.** `Scene = route + forced state`. A screen with four states is
   four scenes; a screen with three dialogs is four scenes. This is what makes "show the modals
   separately" fall out of the design rather than being a special case.
3. **The scene registry is also the state catalogue.** Because a scene names its own state, a scene id
   is a shareable deep link: `?ui=compose.logsOpen=1#/projects/1/env/1/compose/1` opens the Logs
   drawer on a cold load. The query comes before the hash because the hash *is* the router —
   `initialThemeMode()` already reads `window.location.search` this way (`app/theme.ts:78`). The
   board's "Open live" uses it, reviewers can paste it, and the capture pipeline drives it. One
   mechanism, three consumers.
4. **The board owns navigation.** The sider menu is rendered once on the canvas as a real (inert)
   miniature, and lines run from each menu item and each tab bar to the frame it shows. Navigation
   becomes visible instead of being something the reviewer has to hold in their head.

### What a reviewer gains

- Every state on screen at once, in menu order, with the modal that belongs to a screen sitting beside
  it rather than hidden behind a click.
- A stable **scene id** per frame to quote in feedback.
- **Open live** on any frame, landing on that exact state.
- The whole board is one page: pan, zoom, filter by group, print.

### What we give up

- **Interactivity inside a frame.** A frame is an image; you cannot type in it. The mock pages stay
  interactive for development, and every frame carries a link to its live equivalent.
- **True liveness.** An image is a snapshot; if the mock changes, the image is stale until re-captured.
  `## Shipping the board` and the manifest below make staleness visible instead of silent.

## The scene model

### Registry

```
web-design/src/scenes/registry.ts

type Scene = {
  id: string;                 // stable slug: 'compose-logs-fatal'
  title: string;              // 'Compose — Logs drawer'
  group: SceneGroup;          // menu group, drives the column the frame lands in
  kind: 'screen' | 'overlay' | 'feedback';
  hash: string;               // the route, from router.ts's paths
  ui?: Record<string, unknown>;  // forced state, keyed as useUiState keys
  viewport: { width: number; height: number };
  dark?: boolean;             // also capture this one in dark (see Open questions)
  caption: string;            // one line: what to look at, and why it is this way
  menuPath: string[];         // ['Projects', 'my-saas', 'production', 'web'] — ordering + flow
};
```

`menuPath` does the ordering ("เรียงหน้าตามเมนู") and gives the flow layer its anchors without any
hand-placed coordinates.

### Forcing state without a click

```
web-design/src/app/uiState.ts

const [open, setOpen] = useUiState('compose.logsOpen', false);
```

`useUiState(key, initial)` is `useState` plus one rule: a value present in the override map wins over
`initial`. The map is read **once, at module load**, from the URL
(`?ui=compose.logsOpen=1&ui=compose.tab=logs`), by the harness module `main.tsx`
imports first. Read-once is deliberate: it makes the override a *scene* mechanism, not a second router
— it takes effect on the next load, which is exactly what a capture, an **Open live** click and a
pasted URL all are, and nothing has to listen for `popstate` or race the hash router. The cost is that
editing `?ui=` in a live tab does nothing until reload; the spec should not claim otherwise. A `ui`
key that no scene declares is a dev-mode warning, because the board is the only consumer of these keys
and a typo has to be loud rather than a silent no-op.

**What converts, and what does not.** The hook is for state that *selects what is rendered*. Measured
against the mock as it stands:

| file | `useState` sites | to `useUiState` (expected; ticket 01 confirms per site) |
| --- | --- | --- |
| `pages/VolumeExplorerPage.tsx` | 13 | 11 — dialogs, editor, refusals; `fileInput` is a ref and stays |
| `data/store.tsx` | 8 | 0 — store seed state goes through `SceneOverrides`, below |
| `pages/ComposePage.tsx` | 8 | 7 — logs drawer, fullscreen, delete, dirty draft, log filters |
| `pages/ProjectsPage.tsx` | 6 | 5 — view, filter, modals |
| `components/VariablesPanel.tsx` | 6 | 5 — modal, bulk paste, query, editing row |
| `pages/ProjectPage.tsx` | 5 | 4 |
| `components/ServiceMonitor.tsx` | 4 | 2 — window and series selection |
| `app/AppShell.tsx` | 2 | 2 — palette, refreshing |
| 14 more files (`MonitoringPage` 3, `StartHandleDialog` 3, `ComposeVolumesTab` 3, …; `ScreensGalleryPage`'s 4 excluded — that page is deleted by 08) | 28 | 20 |
| **total** | **80** | **≈55** |

Excluded by rule, not by taste: derived values that should be `useMemo`, imperative handles (`useRef`,
`Form.useForm`), and the router's own state (the hash already is the source of truth). Ticket 01's
first output is this table filled in per site — it is what keeps "≈55" from drifting into "every
`useState` in the app", and it is reviewable before a single screen is touched.

antd helps here: every overlay accepts a controlled `open`, so a dropdown, tooltip, popconfirm or
modal is forceable without touching its internals (`Dropdown open`, `Popconfirm open`, `Select open`).
Storage-level state — which handle is open, the mock latency, the activity log — is forced through a
`SceneOverrides` object passed into `PanelProvider` as a prop (`data/store.tsx:103`, the component's
own signature — its `useState` calls are the seed values), not through the hook: an override is a
prop, and "back to normal" is the seed.

Two families of state need a small fiction, and it is worth being explicit about both:

- **Toasts.** `message.success` disappears after 3 s. A scene that wants to show "Saved
  my-saas / production / web" calls it with `duration: 0` on mount, so it stays for the capture.
- **Post-action states.** Form-validation errors only exist after a submit. A scene declares
  `after: 'submit'` and the adapter performs the store/UI transition on mount rather than painting a
  fake. The frame then shows a state the code can actually produce.

### A frozen clock

`?clock=2026-09-26T09:00:00Z` fixes the mock's notion of "now". The wall clock is read in six places
and every one of them has to see the fixed value, so the first draft's list of three was short:

| site | what drifts without it |
| --- | --- |
| `app/useNow.ts:11,14` | every relative label and the idle countdown ("closes in 8 min") |
| `data/store.tsx:121,128` | the seeded handle's `openedAt` / `startedAt` |
| `data/store.tsx:233` → `data/mocks.ts:526` | `metricSamples(window)` defaults `now = Date.now()`; the Monitor charts' axis labels *and* shape |
| `data/volumeMocks.ts:26` | every file's `modifiedAt` in the Volume Explorer |
| `pages/GuidelinesPage.tsx:15,24,28` | the guidelines page's activity feed and deploy demo |
| `data/mocks.ts:641` | the log fixture's relative timestamps |

Three rules fall out of that list, and all three are cheap now and expensive later:

1. **The clock installs before the module graph evaluates, not in `main.tsx`'s body.**
   `volumeMocks.ts` calls `mtime()` at module scope through `file()`/`dir()`/`symlink()`, so the
   fixture timestamps exist before any statement of `main.tsx` runs. The harness (`?clock=`, `?ui=`,
   `?scene=`) must therefore be the **first import** of `main.tsx` — `import './app/scene'` — with the
   hook and the store overrides re-exported from it, and a comment saying why the order matters.
2. **Patch, don't plumb.** The harness patches `Date.now` and `new Date()`'s no-argument form for the
   life of the page; the six call sites above need no edit at all. That keeps the diff on the state
   that has to *change* (the `useUiState` swap) and leaves the fixtures alone.
3. **Capture runs pin `TZ=UTC`.** `mocks.ts`'s `hourOfDay()` and dayjs both render in local time, so the
   same frozen instant draws a different chart and different "3 minutes ago" labels on two machines.
   The clock makes a frame reproducible *given* the timezone; the runner is what pins it.

## Scene inventory

First cut, from a read of every page and component. The exact list is ticket 03's output and is
reviewed as a list before anything is captured — expect the count to move by ±10.

**Shell and navigation** — the canvas's flow lines start here.

| id | what it shows |
| --- | --- |
| `shell-light`, `shell-dark` | the shell itself: sider, breadcrumbs, header cluster |
| `shell-account-menu` | account dropdown open |
| `shell-command-palette` | command palette open |
| `shell-command-palette-empty` | palette filtered to nothing |

**Overview** (`#/`)

| id | what it shows |
| --- | --- |
| `overview` | attention alert, live-handle banner, activity, all Composes |
| `overview-all-clear` | no attention: "All clear" tag + Empty |
| `overview-refreshing` | Refresh spinner mid-flight |
| `overview-handle-orphaned` | banner naming "a deleted Compose" |

**Projects** (`#/projects`) — 8 scenes

| id | what it shows |
| --- | --- |
| `projects-cards`, `projects-table` | the two views |
| `projects-filtered-empty` | "No Projects match "billing"" |
| `projects-empty` | nothing yet |
| `projects-row-menu` | row dropdown open |
| `projects-new-modal` | create modal (+ its validation error as a second scene) |
| `projects-edit-modal` | edit modal, prefilled |
| `projects-delete-modal` | typed-name confirm |

**Monitoring** (`#/monitoring`) — 4 scenes: default (memory in `warn`), loading skeletons, 24 h
partial ("collecting — 187 of 1440"), and a 15 m all-collected window for contrast.

**Project** (`#/projects/:p/env/:e/:tab`) — the widest screen, ~20 scenes. Composes tab: default,
empty env (`preview`, `#/projects/1/env/3/composes`), row dropdown, New Compose modal (empty YAML →
OK disabled, then filled), New Environment modal, Delete Compose confirm, 404. Variables tab: table,
masked→revealed, variables-empty (`#/projects/1/env/3/variables`), filtered-to-nothing, New / Edit
variable modals, paste-a-`.env` with preview, paste with duplicate-key error, delete popconfirm.
Settings tab: form, environments table, delete-env popconfirm, danger zone, Delete Project confirm,
zero-environments empty state.

**Compose** (`#/projects/:p/env/:e/compose/:c[/:tab]`) — ~22 scenes

| pane | scenes |
| --- | --- |
| Editor | running (`compose/1`), partial + exited service (`compose/2`), never deployed (`compose/3`), dirty/unsaved with the Discard/Save banner, 404, More-menu open, Delete confirm |
| Monitor | a real chart, empty ("Nothing to monitor yet"), loading, partial (`collecting — 0 of 15`, the exited worker-cron case) |
| Volumes | resolvable with a live handle, not resolvable (`compose/3`), nothing mounted (`compose/4`), resolving skeleton |
| Logs drawer | filled, the fatal worker-cron exit, loading, empty (`compose/3`) |
| Fullscreen editor | open with the variables rail, rail filtered to nothing |

**Start-handle dialog** — 5 scenes, because each has its own warning: rw, read-only, "cannot check
whether this path exists" (`unverifiable`), the absent-path refusal (no fixture today — see Risks),
and rw + running services, which is the banner the operator most needs to read before writing.

**Volume Explorer** (`#/.../volumes/<id>`) — ~20 scenes: root listing (escaping symlink, too-large
file, newline filename), a subdirectory, no handle open, handle closed by idle, unreadable directory
(`/avatars`), volume not found, compose not resolvable, loading, read-only banner, running-stack write
warning, file editor open, too-large refusal, upload refused (size / free space), new file / new
folder modals, download-as-archive dialog with the symlink disclosure, row dropdown, delete
popconfirm, empty directory.

**Auth** — 7 scenes: sign in, submitting, validation error, setup step 1, step 1 with errors, step 2
"you're set up", not-found.

**Feedback and states** — ~10 scenes salvaged from `#/design/states` for the states that have no home
screen: first-paint skeleton, action-in-flight, nothing-yet, filtered-to-nothing, inline error with a
retry, page-level 500, form validation, outcome toasts and a keyed notification, destructive
confirmations, the 360 px narrow viewport. The volume-specific sections of that page are dropped — they
are covered by real Volume Explorer scenes now.

**Design system** — `guidelines-overview`, plus the three chart states the guidelines page is the only
place to see (`partial`, `unavailable`, auto-axis with a >100 % worker series).

Roughly **95–105 frames**, of which about a third are overlays. Capture cost at ~6 s each is 10–15
minutes for a full run, which is why `--only` exists.

## The canvas

### Layout grammar

- One **column per menu group**, left to right in menu order: Overview, Projects, Monitoring, Project,
  Compose, Volume Explorer, Design system, Auth, Feedback. A group header sits at the top of each.
- Within a column, frames stack in reading order. A screen's **overlays sit in a rail to its right**,
  indented, so "Compose — running" and the three dialogs that belong to it read together.
- Every frame is laid out at its captured pixel size (from the manifest), so nothing reflows when
  images load.

### Flow lines

Two kinds, and only two — a third would be decoration:

- **nav** (solid): a sider menu item, or a tab inside a screen, → the frame it shows. Sources are the
  inert menu miniature and each frame's declared tab anchors; destinations are column/frame boxes.
- **opens** (dashed): a trigger inside a screen → the overlay frame it opens. Anchors are declared as
  fractions of the frame box (`{ scene: 'projects', at: [0.97, 0.04] }`), which is how "the ⋯ button in
  the header" is expressible without pixel-picking.

Lines are SVG beziers in the same transformed space as the frames, with
`vector-effect="non-scaling-stroke"` so they stay 1 px at every zoom. A legend sits in the board's bar,
and the whole layer toggles off — a reviewer checking spacing does not want lines over the frames.

### Board chrome

Sticky bar, outside the transform: theme (Light/Dark, per-frame where a dark still exists), group
filter, search over scene titles and ids, zoom presets + Fit + `100%`, **lines** and **captions**
toggles, a legend, and Print. The manifest's source revision is shown in the corner, so staleness is
always on screen.

### Frame chrome

Under each frame: scene id (monospace, copyable), title, route, and **Open live**. Clicking the image
opens a lightbox at 100 % — the frame itself never navigates.

### Pan and zoom

Wheel zooms to the cursor, drag pans, `Fit` frames a group, `0`/`+`/`-` for the obvious. A minimap is
a stretch, not part of the plan.

## The capture pipeline

`web-design/scripts/stills.mjs` — Node 24, no new dependencies, CDP over the global `WebSocket`.
Same instrument the gallery was verified with (`.scratch/volume-files/issues/01-design-pass.md` drove
Chrome over CDP to assert on the 18 frames; `host-monitoring` and `design-port` both kept a file:// CDP
harness for their render checks). Plain `chrome --screenshot` is not the tool for *this* job because
the board needs scripted waits and whole-page capture, and because `scripts/screenshot.sh` cannot shoot
a surface too tall for one viewport — the same reason it already excludes `#/design/screens`
(`scripts/screenshot.sh:9`). It is not a paint failure: the `volume-files` ticket that built the gallery
recorded all its routes rendering non-blank with that script.

**Capture runs against a production build, not `vite dev`.** `main.tsx` wraps the app in
`<StrictMode>`, and React 19 double-invokes effects in development — the mount-time `after: 'submit'`
adapters and the `duration: 0` toasts above would fire twice, the store would transition twice, and
`data-scene-ready` would flap. The pipeline therefore drives `vite preview` (or the built
`dist-single/`), where effects run once; `pnpm dev` remains for humans.

For each scene:

1. `Emulation.setDeviceMetricsOverride` at the scene's viewport (default 1600×1000), `deviceScaleFactor` 1.
2. Navigate to `?scene=<id>&theme=<mode>&clock=<fixed>#<hash>` — query before hash, as above. `scene=`
   is the pipeline's own assertion: readiness reports the id back, so a capture cannot silently be of
   the wrong scene.
3. Wait for readiness: `pending` empty in the store, `document.fonts.ready`, and — for scenes with an
   editor — Monaco mounted and idle. The app publishes this as `data-scene-ready` on `<html>`.
4. Inject `animation: none !important; transition: none !important; caret-color: transparent`, so a
   shimmer, a tooltip fade or Monaco's blinking cursor cannot smear the frame.
5. Measure `scrollHeight`, then `Page.captureScreenshot({ clip: {…height: scrollHeight}, captureBeyondViewport: true })`
   at the real viewport height — so `clamp(640px, 80vh, 1000px)` (`theme.ts:37`) resolves against a
   genuine viewport while the image still shows the whole page.

Two consequences of step 5 that have to be accepted knowingly, because the gallery's header argues the
opposite for its own frames:

- **The image is content-height, not viewport-height.** `#/design/screens` refused content-height
  frames on the grounds that "the composition a reviewer approved would be one no real viewport ever
  shows". The board is a catalogue of states rather than a viewport review, so the whole page in one
  frame is the right call *here* — but the rule that changes is worth a line in `GUIDELINES.md` rather
  than a contradiction between two files.
- **Beyond-viewport capture plus `position: sticky` and viewport-tall overlays is the fragile
  combination.** `AppShell`'s header is sticky (`app/AppShell.tsx:221`), antd's Drawer/Modal/message are
  fixed — a drawer should come out viewport-tall inside a taller image (that is honest), but the sticky
  header and any scroll-position artefact must be looked at once, on one screen and one drawer, before
  ~100 frames are committed to the pipeline. Ticket 04's first task is that smoke test.

Then encode to WebP — `Page.captureScreenshot({ format: 'webp' })`, i.e. Chrome's own encoder, which
has no lossless switch: **quality is an integer and the output is lossy.** So "lossless" is not
available for free and the size estimate has to be measured, not asserted: capture one 1600×1000 text
screen at `quality: 90` and at default, record bytes and milliseconds, and compare against PNG (which
for UI text compresses worse but rings less). Ticket 04 records the numbers in its Comments, and the
`## Shipping the board` budget only moves once they exist.

Write `public/stills/<id>[@dark].webp` and the manifest — which is a **source** file, not a public
asset, so it can be imported and typechecked rather than fetched (see below):
`src/scenes/manifest.json`, one entry per still:

```
{ id, theme, file, width, height, bytes, sha256, capturedAt, sourceRev }
```

`sourceRev` is `git rev-parse HEAD` of the mock's tree plus a dirty flag, so a board built from a
modified working tree says so.

Flags: `--only <id…>`, `--group <group>`, `--theme`, `--scale`, `--check`.

**`--check` is a same-machine check and the spec should say so.** It re-captures into a temp dir and
compares hashes, which costs a full 10–15 minute run (so it is not a per-edit loop, and this repo has
no CI to put it in — there is no `.github/workflows`), and byte equality also breaks on a Chrome or
font update. Its honest use is "did the mock change since the last capture, on the machine that made
them", and the cheap alternative for everything else is `sourceRev`: if HEAD or the dirty flag moved,
the board's corner says the stills are behind. That is the staleness signal that costs nothing.

**A missing still is a visible hole, never a silent one.** The board renders a labelled placeholder
frame for a scene the manifest does not cover, so a registry entry added without a capture is obvious
on the board. The same rule applies to a missing **dark** variant: the theme toggle falls back to the
light still and marks the frame, rather than showing an empty box.

## Shipping the board

Two artifacts, and the difference between them is what the first draft left implicit:

- **The board is the mock's `#/design/board` route.** One implementation of the canvas, not a second
  app — Decision 1's "the board does not grow into an app" held to.
- **The offline copy is a built *folder*, not a file.** `pnpm build:single` writes
  `dist-single/index.html` (already ~5.5 MB today) and copies `public/stills/` beside it, so the offline
  review path is: somebody who has Node builds once, then anyone — no Node, no Chrome — opens
  `dist-single/index.html#/design/board` from disk. `dist-single/` is gitignored
  (`web-design/.gitignore`), so that folder is handed over, not committed. The link to give a reviewer is
  `dist-single/index.html#/design/board`.

Three details make it work, and each is a trap if it is left to the implementer:

1. **The manifest is imported, never fetched.** `fetch()` from a `file://` page is blocked, so a board
   that reads `public/stills/manifest.json` at runtime would look perfect in dev and render 100 holes
   offline. `stills.mjs` writes it to `src/scenes/manifest.json`, which the board imports like any other
   module — inlined by both builds, typechecked, no request.
2. **Stills are referenced relatively, never imported.** `public/` is copied verbatim into `outDir`, and
   `stills/<id>.webp` resolves from `file://`; but `import.meta.glob`-ing the images would trip
   `assetsInlineLimit: 100_000_000` (`vite.single.config.ts:14`) and base64 the entire set into one HTML
   (≈ +33 % on top of ~10 MB of WebP, added to 5.5 MB). Manifest imported, images not.
3. **Stills are committed; the built folder is not.** Commit `web-design/public/stills/**` (~100 WebP).
   The design pass *is* the deliverable, a reviewer must be able to see it without regenerating it, and
   the alternative — a gitignored `stills/` rebuilt by `pnpm stills` — makes the review surface depend
   on a 10–15 minute run on every machine that wants to look.

**Budget, once measured.** The first draft claimed "WebP lossless at ~8–12 MB for ~100 frames"; CDP's
WebP is lossy (above), so that figure is unmeasured and looks optimistic for 1600×~2400 text screens.
Ticket 04 measures one frame — bytes at `quality: 90` and default, versus PNG — and the whole set,
*before* this recommendation is treated as settled: comfortably under ~15 MB, commit them; over, drop
the frames' quality, cut the dark subset, or ship the set as a release artifact and say so in the
README. Discovering it after ~100 MB of WebP has landed in git is the expensive order.

**ADR.** "The review surface is stills committed to the repo" is a durable, repo-wide decision of the
kind `docs/adr/` exists for (compare 0004, 0007, 0008). Ticket 08 writes it, with the measured sizes in
it, and cites it from `README.md` and `GUIDELINES.md`.

## What happens to the existing pages

The board replaces both design surfaces **as review surfaces** — that is what "one review surface"
means, and it is settled here rather than left as an open question.

- `#/design/screens` is **removed**: the route, `pages/ScreensGalleryPage.tsx`, `paths.screens`, the
  `screensGallery` branch in `main.tsx` (and its `bare` entry), the route kind in `router.ts`, the
  gallery's comment header, and the README prose. The board takes its place at `#/design/board`, and the
  menu's Design system group becomes *Still board* and *Guidelines*.
- `#/design/states` **stops being a review surface but keeps its route**, because it is the only place
  the ~10 feedback scenes with no home screen exist: relabelled (its title stops claiming to be the
  states gallery), volume-specific sections deleted as duplicates of real screens, and it stays
  reachable for capture without a menu item of its own. `paths.states` and `GuidelinesPage.tsx:586`'s
  link survive; only the link's wording changes.
- `scripts/screenshot.sh` keeps working and gains nothing — its `states:/design/states` line still
  resolves, which is the check that the previous bullet is true rather than a claim.
- The prototype pages themselves are untouched apart from the `useUiState` swap — they remain the mock,
  they remain interactive, and they remain the thing scenes are captured from.
- `web-design/README.md` and `GUIDELINES.md` §4.9 are updated to describe the board (§4.9 also records
  the content-height-frame rule the board changes, so the two files stop contradicting each other).
- `volume-files` ticket 01's review surface is rewritten to point at the board; the review gate itself
  does not move — the volume implementation still waits for approval.

## Cost, risks, and how each is contained

| Risk | Containment |
| --- | --- |
| ~55 `useState` → `useUiState` edits across ~17 files | Mechanical, one hook, mock-only, reviewed per screen *and* against the per-file inventory (see *What converts, and what does not*), which is what bounds it. It is the price of addressable states, and it pays off outside the board (deep links). |
| The clock is patched too late to matter | The six call sites are listed, and one runs at import time — so the harness is `main.tsx`'s first import, not a hook or a provider. |
| `StrictMode` double-fires the mount-time fictions | Capture drives a production build (`vite preview` / `dist-single`), never `pnpm dev`. |
| Images go stale as the mock changes | Manifest carries `sourceRev` (HEAD + dirty flag); the board shows it, and that is the cheap signal. `pnpm stills --check` is a same-machine, 10–15 minute re-capture that byte comparison also invalidates on a Chrome update — so it is a milestone tool, not a guard, and the repo has no CI to put it in. |
| Capture is slow (~10–15 min for ~100 frames, estimated) | `--only` / `--group` for iteration; full runs are for review milestones. Ticket 04 makes the pilot capture (two screens, four frames) real so the number stops being a guess before ~96 more frames are taken. |
| A frame captures mid-animation or mid-load | The readiness signal plus the animation-freeze injection, and a scene cannot capture unless `data-scene-ready` reports its own id. |
| Beyond-viewport capture mangles sticky/fixed chrome, or looks nothing like a real viewport | Sticky header (`app/AppShell.tsx:221`) and viewport-tall overlays are smoke-tested on one screen and one drawer first, and the content-height rule is written into `GUIDELINES.md` §4.9 so it is a decision rather than a contradiction. |
| WebP is lossy, so "lossless, 8–12 MB" may be wrong by an order of magnitude | Measured in ticket 04 before the set is committed; *Shipping the board* names the fallbacks (quality, dark subset, release artifact). |
| The offline promise quietly does not hold | Shipping path is explicit: manifest imported (not fetched — `file://` blocks it), images relative, `dist-single/` folder handed over. |
| Dark mode doubles the work | `dark: true` opt-in per scene, not global (open question 1 below). |
| Some states have no fixture to reach them | `ComposeVolumesTab`'s disabled-Open state and `StartHandleDialog`'s absent-path refusal have no fixture at all today. Either add the fixture or document the state as unrendered — the board must not draw a frame the app cannot produce. |
| Removing `#/design/screens` ripples further than the page | The route kind, `paths.screens`, `main.tsx`'s branch and its `bare` entry, and the README prose are all named in *What happens to the existing pages*. `#/design/states` **survives** as the host for scenes with no home screen, so `screenshot.sh` and the guidelines link need no change — removing it would strand ~10 feedback scenes. |
| The board itself grows into an app | It renders declared data and draws two kinds of lines. No comments, no approval workflow, no editing (non-goals). |

## Delivery

Tickets in `.scratch/design-board/issues/`, in dependency order. Two review points rather than one:
**ticket 03's registry** (the scene list is agreed as a list before anything is captured) and
**ticket 04's pilot capture** (four frames, agreed before the other ~96).

| # | Ticket | Blocked by |
| --- | --- | --- |
| 01 | `01-scene-harness.md` — the harness: `?ui=` / `?clock=` / `?scene=`, `useUiState`, `SceneOverrides`, frozen clock | — |
| 02 | `02-addressable-states.md` — the swap across the mock, plus the toast and post-action adapters | 01 |
| 03 | `03-scene-registry.md` — every scene declared, in menu order, with captions and links | 02 |
| 04 | `04-stills-pipeline.md` — pilot capture, readiness, determinism, WebP, manifest, measured sizes | 01, 03 |
| 05 | `05-the-board.md` — layout, pan/zoom, frame chrome, lightbox, Open live, missing-still holes | 03, 04 |
| 06 | `06-flow-layer.md` — menu miniature, anchors, SVG connectors, legend | 05 |
| 07 | `07-board-chrome.md` — theme (incl. the dark subset), filters, search, print, revision corner | 05 |
| 08 | `08-ship-and-retire.md` — offline path, remove `#/design/screens`, README, GUIDELINES, ADR, volume ticket 01 | 05, 06, 07 |

01 and 02 are split because they have different failure modes: the first is a new module with one hard
ordering constraint and one determinism acceptance test, the second is ~55 reviewed edits with no new
concepts. Together they were the plan's single long pole; apart, each is a ticket someone can finish and
verify on its own.

## Verification and testing decisions

`web-design/` has no test runner today and this plan does not add a dependency to give it one, so
verification is the folder's own established recipe plus one thing Node 24 ships for free:

- **`pnpm --dir web-design typecheck`** — the mock's existing gate, and the one that catches the
  `useUiState` swap's mechanical mistakes.
- **`node --test scripts/*.test.mjs`** — `node:test` is stdlib on Node 24, so the board's *pure* rules
  (menu ordering from `menuPath`, column/rail placement, anchor fractions → canvas points, connector
  paths, manifest ↔ registry diff) get real unit tests with no new dependency. This is where "the board
  is declared data plus arithmetic" is cashed in: the layout module stays render-free.
- **The CDP render check** — the technique every ticket in `design-port`, `host-monitoring` and
  `volume-files` used: drive the built bundle, assert on the DOM, screenshot the notable states. Each
  ticket names what it asserts: ticket 02 that every override key produces the state it names, ticket 04
  that a scene cannot be captured unless readiness reported *its* id, ticket 05 that every registry entry
  has a frame and that a still missing from the manifest renders the labelled placeholder.
- **Determinism is itself a test, once.** Ticket 01's acceptance is that one scene captured twice, a
  minute apart on the wall clock with the same `?clock=`, produces byte-identical WebP. Without that
  check the frozen clock is a claim rather than a fix.
- **The review gate is not automated.** The board is approved by the user looking at it (non-goals); no
  ticket may treat "the stills exist" as approval of the design.

## Open questions

Settled while reviewing this plan, so no ticket is built on a guess:

- **Does the board replace both `#/design/screens` and `#/design/states`?** As *review surfaces*, yes —
  see *What happens to the existing pages*. `#/design/states` keeps its route as the host for the ~10
  feedback scenes that have no home screen, which is a correction to this plan's first cut: deleting it
  would have stranded them.
- **Commit the stills?** Yes, subject to the measured budget gate in *Shipping the board* — the decision
  is "commit", the size is what ticket 04 measures.
- **All overlays as frames, or only the primary ones?** All (~35). "Show the modals separately" is the
  user's ask; trimming to the primary ~20 remains a one-line filter if the board reads as crowded.
- **The clock's scope** — all six call sites, patched rather than plumbed, with `TZ=UTC` pinned by the
  runner.

Still open, and cheaper to answer now than after capture:

1. **Dark mode coverage** — a chosen subset (`dark: true` on the ~15 frames where dark actually matters)
   or all ~100? Recommended: the subset, decided while writing the registry in ticket 03, since doubling
   the captured set doubles every full run.
2. **Frame chrome contents** — scene id (monospace, copyable), title, route, caption, **Open live**.
   Anything else wanted per frame, e.g. a one-line "what to check" for the reviewer, or the group and tab
   the frame sits under? Every addition is paid on all ~100 frames.
3. **Pilot first, or capture everything first?** Recommended: ticket 04 captures Overview and one Compose
   scene in light plus one overlay (four frames), and ticket 05 renders those four before the other ~96
   are captured — so the frame chrome, the image quality and the WebP budget are agreed on four images
   instead of a hundred.

## Non-goals

- Not a replacement for `GUIDELINES.md` — that stays the written contract; the board shows it next to
  the screens it describes.
- No commenting, approval, or per-frame sign-off on the board. Feedback stays in conversation, keyed by
  scene id.
- No pixel-diffing between captures.
- No automation of the review gate: approval is still the user's, on the board.
