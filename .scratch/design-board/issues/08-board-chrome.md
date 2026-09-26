# 08 — Board chrome: the switch, filters, search, print, revision

**What to build:** The sticky bar above the canvas — outside the zoom transform, so controls stay
readable at any zoom — carrying everything a reviewer needs to *narrow* a hundred-and-twenty-odd frames
down to the question they are asking:

- **Theme switch**: on, it themes the **board's own chrome** (bar, columns, frame cards, captions, lines)
  and every **Open live** link starts carrying `?theme=dark`, so the dark design is one click away on the
  live mock at the frame's exact state. There are no dark stills (see *Dark mode is a switch*), the frame
  images never change, and the lightbox says so in one line — a dark board must not read as dark
  screenshots.
- **Device filter**: Desktop / Mobile / Both, defaulting to Both — with 06's mobile rail this is what lets
  a phone be reviewed without losing the desktop frame it pairs with.
- **Group filter**: one chip per `SceneGroup`, plus `kind` (screen / overlay / feedback) — "just the
  Compose overlays" has to be one click.
- **Search** over scene ids, titles and captions.
- **Zoom**: presets, `Fit` (per group), `100 %`.
- **Toggles**: lines (07's layer), captions.
- **Legend** for the two link kinds (rendered here, owned by 07).
- **Print**: the frames in menu order, captions and ids included, controls and connectors excluded —
  the thing a reviewer attaches to a message. Verified by reading what the print stylesheet does to a
  filtered set, not by trusting a printer dialogue.
- **The manifest's revision in a corner**, always on screen: `sourceRev` (HEAD + dirty flag) and the
  capture date. This is the cheap staleness signal decided in `## Shipping the board`, and it is the one
  place the board admits it is a snapshot.

Filtering, searching and switching are view state of the board itself, not of the mock: they live in the
board's own state and are **not** part of the `?ui=` scene mechanism (`?ui=` describes a mock screen's
state; a filtered board is not a scene).

**Blocked by:** 06

**Status:** needs-triage

- [ ] The bar is sticky and outside the transform: zooming to 50 % does not shrink the controls
- [ ] The switch themes the bar, columns, frame cards, captions and lines immediately, and while it is on
      every **Open live** link carries `?theme=dark` — checked by following three of them and seeing the
      dark design at the state the frame showed
- [ ] No frame's image changes with the switch, and the lightbox states that the stills are light — the
      board must never imply dark screenshots
- [ ] The device filter narrows to desktop-only or mobile-only, and the mobile rail disappears with it
      (and reappears with Both) without reflowing the desktop frames
- [ ] Group, kind and device filters compose, and the lines follow the visible set (hidden scenes drop
      their connectors rather than drawing lines to nothing)
- [ ] Search matches id, title and caption, and a query with no match says so rather than showing an
      empty canvas
- [ ] Zoom presets, `Fit` and `100 %` agree with the wheel/drag behaviour from 06
- [ ] Print renders frames in menu order with ids and captions, and excludes the controls and the flow
      layer; a filtered board prints the filtered set
- [ ] The revision corner shows `sourceRev` and the capture date, and says so when the working tree is
      dirty — checked against a `pnpm stills --only` run made from a modified tree
- [ ] `pnpm --dir web-design typecheck` green; the bar's own state is not smuggled into `?ui=`
