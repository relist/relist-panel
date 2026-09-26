# 03 — Responsive and mobile design: the panel at phone width

**What to build:** The missing design. Today the panel's responsive behaviour is one antd prop —
`Layout.Sider breakpoint="lg" collapsedWidth={0}` (`app/AppShell.tsx:159`) — which below 992px deletes
the sider and leaves **no way to navigate at all**, plus four descriptive sentences in `GUIDELINES.md`
§6 and a static 360px illustration in the states gallery. This ticket turns that into a designed
contract, screen by screen, at **390×844** (phone class) and checked at **360**.

This is a design gate of its own: mobile changes screens far more than the `useUiState` swap does, and it
is reviewed at 390 on the running mock — before the registry (04) can declare a single mobile scene, and
before anything is captured from a phone-width viewport.

**The shell, first** (everything else is judged inside it):

- a top bar at mobile width with a menu button opening the sider as a `Drawer`, so navigation is
  reachable again — `shell-nav` is the scene that exists because of this decision
- the header cluster (search / theme / account) collapses to icons plus an overflow menu
- breadcrumbs truncate to parent + back control; a phone-width title never fights a wrapped action row
- the `PrototypeStrip` (mock latency) stops taking a full row of the viewport

**Then the screens.** For each: what stacks, what scrolls, what is dropped, and what stays in the first
viewport. The known mechanics, from reading the mock rather than guessing:

- **Tables → stacked rows** in the eight files that use `<Table>` (`ProjectsPage`, `VariablesPanel`,
  `ProjectPage`'s environments table, `VolumeExplorerPage`'s file list, `ComposeVolumesTab`, …). Keep the
  one or two columns that carry the decision: the file list keeps name, size and mtime and drops
  uid/gid/mode, which is why its owner columns can go at all.
- **Tab bars scroll, never wrap**: the `Segmented` controls on Projects, Project (env + tabs),
  Monitoring (window) and the Compose `Tabs` bar (Editor / Monitor / Volumes).
- **Dialogs become sheets** below 768px: `Modal`, `DangerModal` (its typed-name control and destroyed-
  resource list included), `StartHandleDialog`, `FullscreenEditor`, and the Volume Explorer's editor.
  `Popconfirm` becomes a confirm dialog whose body names the subject row, since a phone has no hover and
  no room for a popover beside a card.
- **The Compose screen**: rail stacks under the editor; `LAYOUT.editorMinHeight`'s
  `clamp(640px, 80vh, 1000px)` becomes a viewport-based height; the primary action stays in the header.
  Monaco stays editable — a phone is where you paste a fix from a deploy notification — and the
  full-screen editor's fixed 340px variables rail stacks under the file.
- **The Volume Explorer**: toolbar wraps or moves behind one overflow button; the breadcrumb path
  truncates in the middle, never off the end; the file list is the stacked-row case above.
- **Charts**: six `MetricChart`s stack (they already do via `Col xs={24}`), the window control scrolls,
  and the axis labels stay legible at 390 — check the downsample-to-pixels rule still holds when the plot
  is ~340px wide.
- **Hit targets**: nothing interactive below 44px, and none of the in-table `size="small"` actions survive
  as-is on a card row.

**Then the words:** `GUIDELINES.md` §6's four responsive sentences are replaced by this contract (the
breakpoint, what changes, and the rules per pattern), and `StatesGalleryPage`'s static "Narrow viewports"
card becomes a link to the mobile scenes instead of a picture of one.

**Blocked by:** 02

**Status:** needs-triage

- [ ] At 390 and at 360, every screen renders with **no horizontal document scroll**
      (`documentElement.scrollWidth <= innerWidth`) — the sweep is per screen, asserted, not eyeballed
- [ ] Navigation is reachable at phone width: the menu button opens the sider as a `Drawer`, the current
      route is selected inside it, and closing it returns to the screen
- [ ] Every screen's primary action is inside the first viewport at 390 (Deploy on Compose, Save on
      Variables, Start handle on Volumes, …)
- [ ] Every `<Table>` screen shows stacked rows at phone width, with the dropped columns named in
      Comments — a dropped column is a decision, not an overflow accident
- [ ] Modal, `DangerModal`, `StartHandleDialog`, `FullscreenEditor` and both Volume Explorer editors
      render as full-height sheets at 390, with their destructive controls reachable and their
      consequences still readable
- [ ] Tab/`Segmented` bars scroll horizontally and never wrap to a second line
- [ ] Monaco is usable at 390 in the Compose editor and the full-screen editor, and no `clamp()` height
      pushes the editor off the viewport
- [ ] The charts' axis labels are legible at 390 and the plot keeps its min/max on downsample
- [ ] No interactive target is under 44px at phone width
- [ ] Mobile nav state is addressable (`useUiState`), so `shell-nav` can be a scene: the drawer open
      state from a cold load with a query string
- [ ] Nothing regresses at ≥ 992px: the desktop review is the same mock it was before this ticket
- [ ] `GUIDELINES.md` §6 carries the new rules and the states gallery's narrow card points at the mobile
      scenes; `pnpm --dir web-design typecheck` green
- [ ] Comments record what the CDP sweep asserted per screen, and any screen where 390 forced a genuine
      redesign decision (not just a reflow) — those are the ones the reviewer should look at first
