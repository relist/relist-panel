# 07 — The flow layer: menu miniature, anchors, connectors

**What to build:** The reason the board is a *board* rather than a document — navigation drawn on the
canvas. Two kinds of line and only two, exactly as the spec's `### Flow lines` says; a third would be
decoration.

- **`nav` (solid):** the sider menu is rendered once on the canvas as a real, **inert** miniature (the
  actual menu markup, non-interactive, at board scale), and a solid line runs from each menu item to the
  scene it shows, and from each screen's declared tab anchor to that tab's scene.
- **`opens` (dashed):** a trigger inside a screen to the overlay frame it opens, with the trigger
  declared as a fraction of the frame box (`{ scene: 'projects', at: [0.97, 0.04] }`) — that is how "the
  ⋯ button in the header" is expressible without pixel-picking.

Sources and destinations both come from the registry's `SceneLink` declarations (04) and the boxes
`src/board/layout.ts` (06) computes — this ticket adds no data of its own, which is what keeps it small
and reviewable.

One rule about the other viewports: a `nav` or `opens` edge is declared **once per screen**, and it
starts and ends on **desktop** frames. A tablet or phone frame is the same screen at another width, so a
link from it — or a pair of lines converging on it — would say something untrue about navigation. The
tablet and mobile rails get no connectors; they get their width labels.

Rendering rules: SVG beziers in the same transformed space as the frames, with
`vector-effect="non-scaling-stroke"` so a line is 1 px at every zoom; connectors never draw over a frame's
image (`opens` lines terminate at the frame's edge); the whole layer toggles off, because a reviewer
checking spacing does not want lines across the frames; a legend sits in the board's bar naming the two
kinds.

**Blocked by:** 06

**Status:** needs-triage

- [ ] The menu miniature is the app's own menu markup, inert, and every item in it has a `nav` line to the
      frame it lands on
- [ ] Every registry `opens` link draws a dashed line from its declared anchor to its overlay frame, and
      an anchor outside `[0,1]` is a loud error rather than a line to nowhere
- [ ] Lines stay 1 px at every zoom level (`non-scaling-stroke`, checked at 50 % and 150 %)
- [ ] The layer is togglable from the bar, and the legend names both kinds
- [ ] Anchor-fraction → canvas-point maths lives in the render-free module and is covered by
      `node --test`, including the frame-edge clipping that keeps a line off the image
- [ ] A scene with no incoming link is visible as such when the layer is on — orphan frames should be
      findable, since an unreachable state is exactly what this board is supposed to expose
- [ ] `pnpm --dir web-design typecheck` green; the flow layer renders with `--only`-captured pilot frames
      too (i.e. it never assumes the full set exists)
