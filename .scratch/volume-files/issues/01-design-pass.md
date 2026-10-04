# 01 — Design pass: Volumes tab, Volume Explorer, and the handle surfaces

**What to build:** The mock-only design pass for this feature in `web-design/`, following the folder's
established contract (see its README): every screen reachable, every state deliberately rendered,
nothing calling the API. This is the visual contract for everything after it — **no implementation
ticket starts until this is reviewed screen by screen and approved.**

**Blocked by:** None — can start immediately.

Every other ticket in this feature is blocked by this one, because the review gate is deliberate: a
design pass regularly changes decisions that cascade into the spec, and it is much cheaper to find
that out on a mock than in Go.

**Status:** wontfix

## Screens

- [x] **Compose page, third tab — Volumes.** Follows the existing two-tab pattern (General, Logs). Lists only the Volumes that Compose mounts: kind (named volume vs bind), source, target, the container-side path each Service uses, and which Services use it. Per-Volume Start control, the handle's state, and a live-handle badge on the tab itself.
- [x] **Volume Explorer** — a new route in `src/app/router.tsx`, reachable from the Volumes tab and designed to be opened in a new browser tab. Breadcrumb and up-navigation; file list with name, size, mtime, owner uid/gid, mode and kind; symlinks rendered with their target; toolbar for the v1 operations (upload, new file, new directory, download, refresh); Monaco for editing a text file, reusing the same setup as the Compose YAML editor.
- [x] **The start dialog.** Source and resolved path, uid/gid resolved with the override control and a note on where the value came from, read-only vs read-write, and the warnings that belong here: an absolute bind source outside the data directory, and the bind source whose existence cannot be checked so that continuing creates it `root:root 0755`.
- [x] **Overview** — the live-handle banner: each open Volume with its Compose and the idle countdown, and a Stop control.

## States the design must render

Per the folder's practice, these are the point of the pass, not an afterthought — and they should
also appear in `#/design/states`:

- [x] **Before the first deploy**: the Volumes tab unavailable, carrying the real error text (`couldn't find env file`), with a plain-language reason.
- [x] **Not containerized**: the feature unavailable when the panel runs as a bare binary, and how that reads on the Compose page.
- [x] **Handle**: stopped, starting, running with the idle countdown ticking, and expired-by-idle with a way back.
- [x] **Explorer**: loading, an empty directory, a read error, and a symlink that escapes the Volume shown with its target and refused with a clear message.
- [x] **Write, when the stack is running**: the banner that carries the entire safety story for writes, since the write is deliberately not gated. It must name the consequence and the affected Services — design it so it reads rather than gets dismissed by reflex.
- [x] **Write refusals**: delete confirmation (`Popconfirm`, naming the path), upload too large, upload with not enough free space, and a file too large to open in the editor. Each states the limit it hit.
- [x] **Narrow viewport**, with the file list still readable.

## Docs and review

- [x] `src/data/mocks.ts` gains the mock Compose Volumes, a Volume with a realistic tree (nested directories, a symlink that stays inside, one that escapes, a newline-bearing filename), and handle state.
- [x] The **Mock latency** switch exercises the new screens' loading and pending states like every other screen.
- [x] `web-design/GUIDELINES.md` records the new rules: the Volumes tab, the Explorer, the deliberate absence of a write gate on a running stack, and the archive's symlink disclosure.
- [x] `web-design/README.md`'s screen table gains the new routes.
- [x] `pnpm typecheck` passes, and `scripts/screenshot.sh` renders the new routes for review.
- [x] **`#/design/board`** — every screen-state on one canvas (105 scenes, static live frames in menu order, navigation drawn between them), so the review is one board instead of a route table and a dev server. Replaced `#/design/screens`, the 18-frame gallery, per the review-surface rework; see `.scratch/design-board/spec.md`.
- [ ] Reviewed screen by screen on `#/design/board` and agreed before ticket 02 or any other implementation ticket begins.

## Comments

Built, typechecked (`pnpm typecheck`) and rendered (`scripts/screenshot.sh`, all non-blank).
The design is in `web-design/`: `ComposeVolumesTab`, `VolumeExplorerPage`, `StartHandleDialog`,
`LiveHandlesBanner`, `TextFileEditor`, and fixtures in `src/data/volumeMocks.ts`.

**The review surface was `#/design/screens`** (18 live frames) and is now **`#/design/board`** —
a pannable canvas of 105 static live frames in menu order, overlays in rails beside their screens,
navigation drawn between frames, every frame carrying a copyable scene id and an *Open live* link to
the same forced state. Frames render inline and are culled off-viewport, so the board costs what is
on screen, not what exists. The design decision record lives in `.scratch/design-board/spec.md` and
its tickets. Verified by driving Chrome over CDP and asserting on the frames:
`[...document.querySelectorAll('iframe')].filter(f => f.contentDocument?.querySelector('.ant-app'))`
reports 18 of 18 once the page has been scrolled.

Two decisions worth flagging, both taken while building rather than before:

- **The mock opens with one handle already live**, on Compose 1's `uploads`. A handle that outlives the
  visit that created it is the entire reason the Overview banner exists, so one has to be open on a
  fresh load to be reviewable at all — and putting it on the Volume with the demo tree means the
  Explorer opens on its real content instead of the empty state. The empty state lives in the states
  gallery.
- **The Compose page's tabs are Editor / Monitor / volumes**, not the "General and Logs" the
  compose-panel spec describes: the prototype has already moved on (Logs is a header `Drawer`). The
  Volumes tab is the third, with a live-handle count badge.

Two real bugs surfaced only because the screens were rendered rather than merely typechecked, and both
would have been much more expensive to find in the implementation:

- `resolveComposeVolumes` and `listDir` were inline in the store's `value` memo, so their identity
  changed on every pending-state update; the effect that called one re-ran on every `pending` flip and
  React threw error #185, maximum update depth exceeded. They are now `useCallback`s outside the memo,
  on the same contract `loadMetrics` already documented.
- `touchHandle` mints a new handle object on every read, so an effect keyed on the handle object
  re-read the directory in a loop — each read refreshing the clock that triggered it. React does not
  complain about this one, because the awaits break the synchronous update chain; the screen just sits
  on its skeleton forever. The effect is now keyed on *whether* a handle is open.

And one path-convention bug: the fixtures key directories with a leading slash while the router
produces relative paths, so entering any subdirectory listed nothing. Normalized in one place
(`normalizeDirPath`) with a comment saying why.

One correction to the record: the earlier claim that `scripts/screenshot.sh` covered 17 routes was
wrong in a way that mattered — `monitoring` was absent from its route list, so the count was 17
entries against 18 routes and the one screen nothing rendered was also the one screen nothing looked
at. It is in the list now. Building the gallery is what surfaced it, because the gallery's frame list
and the script's route list are the same set written twice.

**Not yet done:** the review. Nothing blocks on tickets 02+ until this is read screen by screen.

**Closed `wontfix` by the re-plan.** The design-review gate this ticket carried is moot: the mock
it would have reviewed was retired per **ADR-0009**, and its build tickets stay in
`.scratch/design-board/` as the record. Review now happens in `web/` during implementation, with
the annotated tag `design-mock` as the visual reference — not a gate. Nothing blocks on this any
more.
