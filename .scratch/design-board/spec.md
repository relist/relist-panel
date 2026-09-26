# Spec — The still board: every screen-state live on one canvas

> **Architecture update — 2026-09-27.** This spec supersedes the stills pipeline sections below
> (*Why stills*, *The capture pipeline*, *Shipping the board*'s manifest rules, and the still-matrix
> open question). The user's direction, in review: **no screenshots** — the board renders its frames
> live, and RAM is bounded by rendering *around the pan position* instead of by capturing. Tickets
> 05 (stills pipeline) is withdrawn; 06–08 are re-scoped in place; everything the harness (01) and
> the audit (02) established carries over unchanged. The superseded sections stay in this file for
> their measurements, which the live design still leans on — read them as evidence, not as plan.

## Problem

`#/design/screens` renders 18 live iframes and asks the reviewer to click around to reach a state.
Most states cannot be seen from it at all (80 `useState` sites, 59 of them gating something visible —
ticket 02's audit), frames have no stable id to give feedback against, and the page says nothing
about navigation — where a menu item or a tab actually leads.

## Decision

One canvas, Figma-style, at `#/design/board` (renders bare, outside `AppShell`):

- **Every screen-state is a frame, rendered live** from the mock in this same document — no iframes,
  no screenshots. A frame is *static*: `pointer-events: none`, its state forced by its scene; modals
  and drawers are separate frames beside the screen that opens them.
- **Off-screen frames are unmounted.** React Flow's `onlyRenderVisibleElements` culls nodes outside
  the viewport; below ~35% zoom a frame degrades to a name card (LOD) so zooming out never mounts
  everything at once. RAM stays bounded by what is visible (~10–24 mounted frames), not by the scene
  count (~100).
- **Frames are ordered by the menu** — one column per menu group, overlays in a rail to the right of
  the screen that owns them — and **navigation is drawn**: solid lines from the menu miniature and
  tab bars to the frames they show, dashed lines from a trigger to the overlay it opens.
- **The engine is reusable.** `src/storyboard/` is React + `@xyflow/react` only — it never imports
  antd, the router, or the mock. `web-design` is its first consumer; it can be lifted into its own
  package once a second consumer exists.

### Why live rendering is safe here — and what the measurement actually says

The measured cost of the *old gallery's* approach — 18 live **iframes** — is 287 MB of JS heap, 19
renderer processes, +~0.5 GB RSS: ~9 MB of heap and a process per frame. That measurement is the
argument against naive live-everything, and it stands. But it prices the iframe, not the frame: a
process per document is what iframes cost. This design renders **inline** — one document, one
renderer — and *culls*: heap tracks the mounted frames the viewport can see, so the number that
matters is heap at max pan, asserted in ticket 06's acceptance (CDP `Performance.getMetrics` +
mounted-node count), not derived here. A frame is a pure function of `(route, forced state)`, so
unmounting and remounting loses nothing — that is the precondition, and the scene harness (01) plus
the swap (02) are what provide it.

## The scene model

A **scene** is one frame: a route plus a forced state. The type, the ordering, the `menuPath`, the
`nav`/`opens` links and the invariants live in `issues/04-scene-registry.md` and are not duplicated
here. `ui` keys are the ones declared in `app/uiKeys.ts` by ticket 02; data-world state goes through
`SceneOverrides`; named `after:` transitions (toasts, post-action states) go through
`app/sceneAdapters.ts`.

### Per-frame isolation — ticket 10

One document hosts N frames, so the harness's module-level read-once state becomes per-scope state:
`SceneScopeProvider` gives each frame its own route state (`useRoute`/`navigate` context-first, the
window hash untouched), its own override map and clock, a nested `<AntApp>` (frame-local toasts with
zero call-site changes), `ConfigProvider getPopupContainer` → the frame box, `getContainer` for
Modal/Drawer, a `--frame-vh` custom property so the few `vh`-based declarations resolve against the
frame, a Monaco model-path prefix (two frames of one screen must not share a model), and an error
boundary — a frame that throws is a red card naming its scene, never a blank board.

## The board

- **Layout**: computed from the registry — columns per menu group in menu order; a screen's overlays
  in a rail to its right; a group header per column; the sider menu rendered once as a real, inert
  miniature whose items source the nav lines.
- **Frame chrome**: scene id (copyable), title, route, caption, and **Open live** — the interactive
  app at the same forced state (`?ui=…#hash`) for when a reviewer wants to touch it.
- **Chrome**: search/jump by scene id, theme switch, viewport switch, lines and captions toggles,
  zoom controls + Fit, minimap, and a "mounted X/Y" readout as the visible proof of culling.
  `?scene=<id>` deep-links to a frame.
- **Two line kinds only**: `nav` (solid) and `opens` (dashed), with a legend; the layer toggles off.
- **Theme is a context flip, not a mode.** Every frame re-themes instantly — the live board gets real
  dark frames, which the stills design had to defer. The viewport switch sets a frame's width for
  real (live frames reflow); desktop is the only width with designed scenes until ticket 03 lands.
- **Offline works.** Inline frames need no dev server: `dist-single/index.html#/design/board` opened
  from disk renders the whole board. `StrictMode`'s dev double-effects are survived by making the
  `after` adapters idempotent and toasts keyed.

## Viewports, and what stays gated

The three-width decision stands (desktop ≥1200 designed at 1600×1000; tablet 768–1199 at 834×1112;
mobile <768 at 390×844, checked at 360). Ticket 03 remains the design gate that changes the screens —
until it lands, the registry declares `viewports` honestly and the board renders desktop scenes only.
Because frames are live, the old question "how wide should the capture matrix be" **dissolves**: a
viewport switch renders any declared width of any scene with no second capture; the only cost of a
width is designing it (03), not photographing it.

## Delivery

| # | Ticket | Blocked by | State |
| --- | --- | --- | --- |
| 01 | Scene harness (`?ui=`/`?clock=`/`?scene=`, `useUiState`, `uiKeys`, readiness) | — | **done** |
| 02 | Addressable states — the swap + adapters, batches per its Comments | 01 | this session, 3 batches |
| 03 | Responsive tablet/phone design pass (changes screens) | 02 | future gate — untouched |
| 04 | Scene registry (desktop scenes now; `viewports` declared, rendered after 03) | 02 | this session |
| 05 | ~~stills pipeline~~ | — | **withdrawn** (superseded by this spec) |
| 06 | The board: `src/storyboard/` engine (React Flow, culling, LOD, scope) + `#/design/board` | 02, 04, 10 | re-scoped |
| 07 | Flow layer (React Flow edges over the declared links) | 06 | stands, re-scoped |
| 08 | Board chrome (theme/viewport switches, search, minimap, mounted counter) | 06 | re-scoped |
| 09 | Ship and retire (`#/design/screens` out, docs, offline path) | 06–08 | stands, simplified |
| 10 | Per-frame scope (`SceneScopeProvider`, context-first harness, containers, `--frame-vh`, Monaco prefix, error boundary) | 01 | **new** — this session |

## Verification

- `pnpm --dir web-design typecheck` — the mechanical gate for the swap.
- `node --test scripts/*.test.mjs` — the pure rules (layout, ordering, anchor math, registry
  invariants) as real unit tests, stdlib runner, no new dependency.
- CDP render checks, per ticket: 02 asserts every forced key produces its state on a cold load;
  06 asserts the board's bounds — mounted frames ≤ ~24 while panning the whole canvas, JS heap flat
  across a full pan, zero error-boundary hits, and `?scene=` landing on its frame; 07 asserts the
  link set is fully drawn; 08 asserts theme/viewport switches with no reload (page-load counter).
- **The review gate is not automated.** Approval is the user's, on the board.

## Non-goals

Interactivity inside frames (Open live covers it) · comments/approval on the board · publishing the
engine before a second consumer exists · pixel diffing · a responsive board surface (it *reviews*
three widths and runs desktop) · touch gestures/PWA.

The volume gate is unchanged: ticket 02 of `volume-files` starts only after the design pass is
reviewed and approved — on this board.
