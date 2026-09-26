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

- **Forcing a state without a click.** Every screen's state has to become addressable. This is
  mechanical (a `useUiState` hook replacing raw `useState`, ~50 call sites) but it is the foundation —
  the board, the capture pipeline, and shareable state URLs all rest on it.
- **Capture determinism.** The mock reads `Date.now()` in several places (`store.tsx:121,128`,
  `app/useNow.ts:11`), so two captures a minute apart differ. A frozen clock fixes it.

Nothing else here is hard. What follows is the detailed plan.

## Decision

Review moves to a **still board**: one pannable, zoomable canvas of static frames — one frame per
screen-state, overlays included — ordered by the menu, with navigation drawn as lines.

Four decisions carry the design:

1. **Frames are captured images, not live iframes.** The scene count is around a hundred, and each
   live frame is a full React root with its own store and its own Monaco instance. The 18-frame
   gallery already weighs what it weighs; a hundred of them is not a review surface, it is a stress
   test. Stills also make the board viewable by someone who has no Node and no Chrome, and they make
   the board *static* in the literal sense the user asked for — nothing to click except the canvas.
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
  `## Stills in git` and the manifest below make staleness visible instead of silent.

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

`useUiState(key, initial)` is `useState` plus one rule: a value present in the current override map
wins over `initial`, and clearing it returns the screen to normal. The override map comes from the
URL (`?ui=compose.logsOpen=1&ui=compose.tab=logs`), applied by `main.tsx` before first paint.

The call sites are the inventory in the next section — every `useState` that gates something visible.
**This is the bulk of the mechanical work in the whole plan**, and it is contained entirely inside
`web-design/`.

antd helps here: every overlay accepts a controlled `open`, so a dropdown, tooltip, popconfirm or
modal is forceable without touching its internals (`Dropdown open`, `Popconfirm open`, `Select open`).
Storage-level state — which handle is open, the mock latency, the activity log — is forced through a
`SceneOverrides` object applied to the store (`data/store.tsx:108`) on mount, not through the hook.

Two families of state need a small fiction, and it is worth being explicit about both:

- **Toasts.** `message.success` disappears after 3 s. A scene that wants to show "Saved
  my-saas / production / web" calls it with `duration: 0` on mount, so it stays for the capture.
- **Post-action states.** Form-validation errors only exist after a submit. A scene declares
  `after: 'submit'` and the adapter performs the store/UI transition on mount rather than painting a
  fake. The frame then shows a state the code can actually produce.

### A frozen clock

`?clock=2026-09-26T09:00:00Z` fixes the mock's notion of "now", read by `app/useNow.ts`,
`store.tsx`'s seeded timestamps, and every relative-time label. Without it, "closes in 8 min" and
"Checked 3 minutes ago" drift between captures and a frame cannot be reproduced.

## Scene inventory

First cut, from a read of every page and component. The exact list is ticket 02's output and is
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

`web-design/scripts/stills.mjs` — Node 24, no new dependencies, CDP over the global `WebSocket`. Same
instrument the gallery was verified with; plain `chrome --screenshot` is not an option, since it was
measured failing to paint this app at all (see `screenshot.sh`'s header, and the ticket-01 comments).

For each scene:

1. `Emulation.setDeviceMetricsOverride` at the scene's viewport (default 1600×1000), `deviceScaleFactor` 1.
2. Navigate to `?scene=<id>&theme=<mode>&clock=<fixed>#<hash>` — query before hash, as above. `scene=`
   is the pipeline's own assertion: readiness reports the id back, so a capture cannot silently be of
   the wrong scene.
3. Wait for readiness: `pending` empty in the store, `document.fonts.ready`, and — for scenes with an
   editor — Monaco mounted and idle. The app publishes this as `data-scene-ready` on `<html>`.
4. Inject `animation: none !important; transition: none !important; caret-color: transparent`, so a
   shimmer, a tooltip fade or Monaco's blinking cursor cannot smear the frame.
5. Measure `scrollHeight`, then `Page.captureScreenshot({ captureBeyondViewport: true })` at the real
   viewport height — so `clamp(640px, 80vh, 1000px)` (`theme.ts:37`) resolves against a genuine
   viewport while the image still shows the whole page.

Then encode to WebP and write `public/stills/<id>[@dark].webp` plus `public/stills/manifest.json`:

```
{ id, theme, file, width, height, bytes, sha256, capturedAt, sourceRev }
```

Flags: `--only <id…>`, `--group <group>`, `--theme`, `--scale`, `--check` (capture to a temp dir and
compare hashes; non-zero exit on drift).

**A missing still is a visible hole, never a silent one.** The board renders a labelled placeholder
frame for a scene the manifest does not cover, so a registry entry added without a capture is obvious
on the board.

## Stills in git

Recommended: commit `web-design/public/stills/**` (WebP + manifest). The design pass *is* the
deliverable, and a reviewer — or the user, on a machine with no Node — must be able to open the board
and see it. WebP lossless at scale 1 puts ~100 frames at roughly 8–12 MB, which is a real cost and the
reason it is an open question below. The alternative is a gitignored `stills/` regenerated by
`pnpm stills`, at the price of the board being unviewable without a build.

## What happens to the existing pages

- `#/design/screens` is **removed**; the board replaces it (`#/design/board`), and the menu's Design
  system group becomes *Still board* and *Guidelines*.
- `#/design/states` stops being a page a reviewer scrolls: its generic sections become scenes (above),
  and its volume-specific sections are deleted as duplicates of real screens.
- The prototype pages themselves are untouched apart from the `useUiState` swap — they remain the mock,
  they remain interactive, and they remain the thing scenes are captured from.
- `scripts/screenshot.sh` keeps working (it is a local aid for one-off routes) and gains nothing.
- `web-design/README.md` and `GUIDELINES.md` §4.9 are updated to describe the board.
- `volume-files` ticket 01's review surface is rewritten to point at the board; the review gate itself
  does not move — the volume implementation still waits for approval.

## Cost, risks, and how each is contained

| Risk | Containment |
| --- | --- |
| ~50 `useState` → `useUiState` edits touch nearly every screen | Mechanical, one hook, mock-only, and reviewed per screen. It is the price of addressable states, and it pays off outside the board (deep links). |
| Images go stale as the mock changes | Manifest carries `sourceRev`; the board shows it. `pnpm stills --check` detects drift. Nothing claims to be live. |
| Capture is slow (~10–15 min for ~100 frames) | `--only` / `--group` for iteration; full runs are for review milestones, not every edit. |
| A frame captures mid-animation or mid-load | The readiness signal plus the animation-freeze injection. This is precisely what `chrome --screenshot` got wrong. |
| Dark mode doubles the work | `dark: true` opt-in per scene, not global (open question). |
| Some states have no fixture to reach them | `ComposeVolumesTab`'s disabled-Open state and `StartHandleDialog`'s absent-path refusal have no fixture at all today. Either add the fixture or document the state as unrendered — the board must not draw a frame the app cannot produce. |
| The board itself grows into an app | It renders declared data and draws two kinds of lines. No comments, no approval workflow, no editing (non-goals). |

## Delivery

Tickets in `.scratch/design-board/issues/`, in dependency order. Ticket 02's registry is the review
point: the scene list is agreed before anything is captured.

| # | Ticket | Blocked by |
| --- | --- | --- |
| 01 | `useUiState` + override parsing + frozen clock, applied across the mock | — |
| 02 | The scene registry: every scene declared, in menu order, with captions | 01 |
| 03 | `stills.mjs`: CDP capture, readiness, WebP, manifest | 01, 02 |
| 04 | The board: layout, pan/zoom, frame chrome, lightbox, Open live | 02, 03 |
| 05 | The flow layer: menu miniature, anchors, SVG connectors, legend | 04 |
| 06 | Board chrome: theme, filters, search, print, manifest revision | 04 |
| 07 | Retire the old surfaces; update README, GUIDELINES, and volume ticket 01 | 04, 05, 06 |

## Open questions

1. **Commit the stills?** Recommended yes (~8–12 MB). The alternative keeps the repo light but makes
   the board unviewable without a build.
2. **Dark mode coverage** — a chosen subset (~15 frames where dark actually matters) or all ~100?
3. **Does the board replace both `#/design/screens` and `#/design/states`?** Recommended yes, with the
   states page's generic demos surviving as scenes.
4. **All overlays as frames, or only the primary ones?** The plan assumes all (~35), which is what
   "modals shown separately" implies; trimming to the primary ~20 is a one-line filter.
5. **Frame chrome contents** — scene id, title, route, caption, Open live. Anything else you want on
   the frame (e.g. a "what to check" line per scene)?

## Non-goals

- Not a replacement for `GUIDELINES.md` — that stays the written contract; the board shows it next to
  the screens it describes.
- No commenting, approval, or per-frame sign-off on the board. Feedback stays in conversation, keyed by
  scene id.
- No pixel-diffing between captures.
- No automation of the review gate: approval is still the user's, on the board.
