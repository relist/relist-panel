# 07 — Board chrome: theme, filters, search, print, revision

**What to build:** The sticky bar above the canvas — outside the zoom transform, so controls stay
readable at any zoom — carrying everything a reviewer needs to *narrow* a hundred frames down to the
question they are asking:

- **Theme**: Light / Dark / Both, per-frame wherever a dark still exists, with the fallback 05 defines
  (light still, marked) for frames that have none. The subset itself was chosen in 03.
- **Group filter**: one chip per `SceneGroup`, plus `kind` (screen / overlay / feedback) — "just the
  Compose overlays" has to be one click.
- **Search** over scene ids, titles and captions.
- **Zoom**: presets, `Fit` (per group), `100 %`.
- **Toggles**: lines (06's layer), captions.
- **Legend** for the two link kinds (rendered here, owned by 06).
- **Print**: the frames in menu order, captions and ids included, controls and connectors excluded —
  the thing a reviewer attaches to a message. Verified by reading what the print stylesheet does to a
  filtered set, not by trusting a printer dialogue.
- **The manifest's revision in a corner**, always on screen: `sourceRev` (HEAD + dirty flag) and the
  capture date. This is the cheap staleness signal decided in `## Shipping the board`, and it is the one
  place the board admits it is a snapshot.

Filtering, searching and theming are view state of the board itself, not of the mock: they live in the
board's own state and are **not** part of the `?ui=` scene mechanism (`?ui=` describes a mock screen's
state; a filtered board is not a scene).

**Blocked by:** 05

**Status:** needs-triage

- [ ] The bar is sticky and outside the transform: zooming to 50 % does not shrink the controls
- [ ] Theme switches light/dark across the whole board at once, frames without a dark still are marked
      and fall back to light, and nothing shows an empty box
- [ ] Group and kind filters narrow the canvas and the lines follow the visible set (hidden scenes drop
      their connectors rather than drawing lines to nothing)
- [ ] Search matches id, title and caption, and a query with no match says so rather than showing an
      empty canvas
- [ ] Zoom presets, `Fit` and `100 %` agree with the wheel/drag behaviour from 05
- [ ] Print renders frames in menu order with ids and captions, and excludes the controls and the flow
      layer; a filtered board prints the filtered set
- [ ] The revision corner shows `sourceRev` and the capture date, and says so when the working tree is
      dirty — checked against a `pnpm stills --only` run made from a modified tree
- [ ] `pnpm --dir web-design typecheck` green; the bar's own state is not smuggled into `?ui=`
