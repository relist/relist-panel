# 12 — Board defaults: content mounts at rest, flow lines start off

> **Follow-up to 06/07 — 2026-09-27.** Ticket 06's acceptance measured the *RAM* side of culling
> (mounted ≤ 4 while panning) but not the cost of what a mount now does: every frame that scrolls into
> view mounts a whole antd screen (shell, charts, Monaco) in the middle of the gesture. That is the
> stutter a reviewer feels, and the fix is the one 06's LOD floor already gestures at: a frame is a name
> card until the camera has settled, and content returns only then. Separately, the flow layer (07) is
> off by default now that `#/design/flow` (11) owns journey reading — a board with its lines on is a
> worse inventory, not a better flow.

**What to build:**

- **Content at rest.** `FrameNode` gains a camera-idle gate: while the board's transform is changing the
  frame renders its `NameCard`, and content re-mounts `CAMERA_IDLE_MS` (180 ms) after the last movement.
  The gate is board-level state passed down a context — a freshly-culled mount must read "moving" too,
  which a per-node timer cannot know. The LOD floor and its hysteresis stay exactly as they are: this is
  a second gate, not a replacement. A `data-frame-content` attribute marks the mounted half so the
  acceptance can count it.
- **Lines off by default.** `BoardPage`'s `lines` default flips to `false`; the toggle stays, and the
  legend stays beside it. The board remains the place to *look at* frames; `#/design/flow` is the place
  to read journeys.

**Blocked by:** 06, 07 — both landed.

**Status:** ready-for-agent

- [ ] Panning the board at review zoom mounts no frame content during the gesture, and frames fill in
      after the camera settles — counted by a headless probe, with the previous numbers recorded
- [ ] At rest the board is unchanged: every frame mounts its content after settling, the LOD floor still
      swaps to name cards below ~35 %, and no frame hits the error boundary
- [ ] The gate does not re-render the board per wheel tick (the idle state only flips at its two edges)
- [ ] `lines` starts off; the toggle, the legend and the `?scene=` deep link behave exactly as before
- [ ] `pnpm --dir web-design typecheck` green; `node --test scripts/storyboard.test.mjs` untouched
