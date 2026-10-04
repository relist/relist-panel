# 02 — Addressable states: the swap, and the two fictions

**What to build:** The `useUiState` swap across the mock — the ≈55 of the mock's 80 `useState` sites
that gate something visible, per the inventory in the spec (`### Forcing state without a click`) — plus
the two families of state that need a small fiction, so the registry (04) is written against states
that can actually be forced. Mechanically repetitive, conceptually empty: the point is coverage, so the
counts in the spec's table are the acceptance list, not a sample.

Ordered smallest-first so the pattern is agreed before the largest file is touched: `AppShell` (2),
`OverviewPage` (1), `MonitoringPage` (3), `ProjectsPage` (6), `ProjectPage` (5), `VariablesPanel` (6),
`ComposePage` (8), `ComposeVolumesTab` (3), `StartHandleDialog` (3), `ServiceMonitor` (4),
`StatesGalleryPage` (the scene host that survives 08) and the remaining files (~20 sites), then
`VolumeExplorerPage` (13, the largest) last. `ScreensGalleryPage` is not converted at all: 08 deletes
the page, so touching its four sites would be work in a file on its way out.

Two families get a fiction, and both are declared rather than painted:

- **Toasts.** `message.success`'s 3 s timeout means a scene can never be captured mid-toast. Scenes that
  want one call it on mount with `duration: 0` through an `after: 'toast:…'` adapter registered here;
  the copy is the app's own, so the frame shows what the app produces.
- **Post-action states.** Validation errors only exist after a submit. `after: 'submit'` (already parsed
  in 01) performs the real store/UI transition on mount — the modal's own submit handler, not a fake
  error object — and readiness waits for it to settle before `data-scene-ready` goes up.

Both live behind a small registry of named adapters in `web-design/src/app/sceneAdapters.ts`, keyed by
the `after` string, so a scene names an outcome and the adapter owns how to reach it.

Mobile comes after this ticket (03) and adds its own keys — `shell.navOpen` and whatever a phone-width
state needs — on top of the same helper; nothing here has to anticipate them, and nothing here should
invent them.

**Blocked by:** 01

**Status:** ready-for-human

- [ ] The spec's per-file table is filled in per site — file, line, key, and the state it forces — and
      attached to this ticket's Comments when work starts; anything excluded is excluded by the stated
      rule (derived values, `useRef`/`Form.useForm`, router state), not by convenience
- [ ] Every converted key is registered in `app/uiKeys.ts`, and forcing it from `?ui=` produces the
      state it names on a cold load
- [ ] Controlled antd overlays are forced with `open` where the component supports it (`Dropdown`,
      `Popconfirm`, `Select`, `Tooltip`, `Modal`, `Drawer`); nothing reaches into an overlay's internals
- [ ] The `after` adapters cover the toast and post-action scenes the spec's inventory lists, and a
      scene that names an unknown `after` value fails loudly in dev
- [ ] Screens are unchanged for a reviewer who never passes a query string: no visible behaviour moves,
      and `pnpm --dir web-design typecheck` is green
- [ ] Per screen, the CDP render check asserts the forced state — the pattern ticket 01 of `volume-files`
      used: drive the built bundle, assert on the DOM, screenshot the state. The states asserted are
      exactly the scene's `ui` keys, so 03 inherits a validated catalogue
- [ ] No behaviour is added to the mock beyond addressability: the swap must not fix, tidy or prune a
      screen. Anything that looks wrong while converting gets recorded in Comments, not fixed here

## Comments

- 2026-09-26 — **Read-only audit** (no source file touched, nothing committed). Produced by a separate
  read-only agent, as ticket 01's Comments anticipated. Every number below is grep output, not recall;
  every claim carries a `file:line`. `web-design/src` was read as it stands on `feat-design-board`
  (working tree clean apart from ticket 01's own `.scratch` edit; `git status --short` showed only
  ` M .scratch/design-board/issues/01-scene-harness.md`).
- 2026-09-26 — **Headline.** The spec's counts hold at the file level: **80 `useState` call sites across
  22 files**, exactly as spec.md:26 claims. The `≈55` does **not** hold: applying the spec's own
  exclusion rule (spec.md:160-163) to each site gives **59 CONVERT, 21 excluded**. The spec's own
  per-file rows sum to **56** (not 55) and three files are one site each under spec:
  `ProjectsPage` 6 not 5, `VariablesPanel` 6 not 5, `ProjectPage` 5 not 4.
- 2026-09-26 — **Method, and how to re-run it.** Call sites (not the loose `useState` grep, which also
  counts 22 import lines):

  ```
  $ grep -rEn '(const|let)\s*\[[^]]*\]\s*=\s*useState' web-design/src --include=*.tsx --include=*.ts | wc -l
  80
  $ grep -rn 'useState' web-design/src --include=*.tsx --include=*.ts | wc -l
  102        # 80 call sites + 22 import lines — the loose grep overcounts by 22
  ```

  Per-file below: `grep -c useState <file>` (includes the import line), the call-site count, the spec's
  site count, my convert count, the spec's convert count.

  | file | `grep -c` | call sites | spec sites | CONVERT | spec convert |
  | --- | --- | --- | --- | --- | --- |
  | `pages/VolumeExplorerPage.tsx` | 14 | 13 | 13 ✓ | **11** | 11 ✓ |
  | `pages/ComposePage.tsx` | 9 | 8 | 8 ✓ | **7** | 7 ✓ |
  | `data/store.tsx` | 9 | 8 | 8 ✓ | **0** | 0 ✓ |
  | `pages/ProjectsPage.tsx` | 7 | 6 | 6 ✓ | **6** | 5 ✗ **+1** |
  | `components/VariablesPanel.tsx` | 7 | 6 | 6 ✓ | **6** | 5 ✗ **+1** |
  | `pages/ProjectPage.tsx` | 6 | 5 | 5 ✓ | **5** | 4 ✗ **+1** |
  | `pages/ScreensGalleryPage.tsx` | 5 | 4 | 4 (excluded) ✓ | **0** | 0 ✓ |
  | `components/ServiceMonitor.tsx` | 5 | 4 | 4 ✓ | **2** | 2 ✓ |
  | `pages/MonitoringPage.tsx` | 4 | 3 | — | **3** | (in the 20) |
  | `components/StartHandleDialog.tsx` | 4 | 3 | 3 ✓ | **3** | (in the 20) |
  | `components/ComposeVolumesTab.tsx` | 4 | 3 | 3 ✓ | **3** | (in the 20) |
  | `pages/StatesGalleryPage.tsx` | 3 | 2 | — | **2** | (in the 20) |
  | `pages/AuthPages.tsx` | 3 | 2 | — | **2** | (in the 20) |
  | `components/MetricChart.tsx` | 3 | 2 | — | **2** | (in the 20) |
  | `components/FullscreenEditor.tsx` | 3 | 2 | — | **2** | (in the 20) |
  | `app/router.ts` | 3 | 2 | — | **0** | router state |
  | `app/AppShell.tsx` | 3 | 2 | 2 ✓ | **2** | 2 ✓ |
  | `pages/OverviewPage.tsx` | 2 | 1 | — | **1** | (in the 20) |
  | `main.tsx` | 2 | 1 | — | **0** | theme switch |
  | `components/DangerModal.tsx` | 2 | 1 | — | **1** | (in the 20) |
  | `components/CommandPalette.tsx` | 2 | 1 | — | **1** | (in the 20) |
  | `app/useNow.ts` | 2 | 1 | — | **0** | `?clock=` |
  | 11 files with no site | 0 | 0 | — | 0 | — |
  | **group: "14 more files"** | — | **28** | 28 ✓ | **20** | 20 ✓ |
  | **total** | **102** | **80** | **80 ✓** | **59** | **56 rows / "≈55"** |

### 1. Per-site inventory

Verdict vocabulary — `CONVERT` = the swap applies; `EXCLUDED — <rule>` uses the spec's stated rules
(spec.md:160-163): derived value, imperative handle (`useRef`/`Form.useForm`), router state, store state
(→ `SceneOverrides`, spec.md:167-170). `CONVERT*` = CONVERT whose value is an **object/array**, so it
cannot be produced by a `?ui=` string as ticket 01 specified it (see §5.2).

**The eight files the spec names**

| file:line | state | proposed `useUiState` key | what it gates | verdict |
| --- | --- | --- | --- | --- |
| `pages/VolumeExplorerPage.tsx:103` | `volumes` | — | skeleton ↔ "Volume not found" ↔ table | EXCLUDED — data resolved from the store/CLI (`resolveComposeVolumes`) |
| `pages/VolumeExplorerPage.tsx:104` | `resolutionError` | `volume.resolutionError` | "Volumes are not resolvable yet" card (`:331-350`) | CONVERT |
| `pages/VolumeExplorerPage.tsx:105` | `entries` | — | the file table rows | EXCLUDED — data (`listDir`); fixture-driven |
| `pages/VolumeExplorerPage.tsx:106` | `browseError` | `volume.browseError` | "Could not read this directory" (`:396-407`) | CONVERT |
| `pages/VolumeExplorerPage.tsx:107` | `startOpen` | `volume.startOpen` | `StartHandleDialog` (`:313-326`) | CONVERT |
| `pages/VolumeExplorerPage.tsx:108` | `editing` | `volume.editing` | the file-editor card + Save/Cancel (`:541-573`) | **CONVERT\*** (`{contents,path}`) |
| `pages/VolumeExplorerPage.tsx:109` | `editorTooLarge` | `volume.editorTooLarge` | "Too large to edit" alert (`:442-457`) | **CONVERT\*** (`VolumeEntry`) |
| `pages/VolumeExplorerPage.tsx:110` | `uploadError` | `volume.uploadError` | "Upload refused" alert (`:459-469`) | CONVERT |
| `pages/VolumeExplorerPage.tsx:111` | `downloadOpen` | `volume.downloadOpen` | Download-as-archive `Modal` (`:598-604`) | CONVERT |
| `pages/VolumeExplorerPage.tsx:112` | `newEntry` | `volume.newEntry` | New file/folder `Modal` + its title/okText (`:575-596`) | CONVERT |
| `pages/VolumeExplorerPage.tsx:113` | `newEntryName` | `volume.newEntryName` | modal input + `okButtonProps.disabled` (`:577`) | CONVERT |
| `pages/VolumeExplorerPage.tsx:163` | `expiredHere` | `volume.expiredHere` | "This Volume Handle closed on its own" (`:375-383`) | CONVERT (clobbered on mount — §5.4) |
| `pages/VolumeExplorerPage.tsx:782` | `format` | `volume.archiveFormat` | download radio + `okText` + filename | CONVERT |
| `data/store.tsx:104` | `projects` | — | every project/compose list | EXCLUDED — store state → `SceneOverrides` |
| `data/store.tsx:105` | `activity` | — | Overview "Recent activity" | EXCLUDED — store state (spec names it, :168) |
| `data/store.tsx:106` | `logs` | — | Logs drawer text | EXCLUDED — store state |
| `data/store.tsx:107` | `checkedAt` | — | "checked N ago" per Compose | EXCLUDED — store state |
| `data/store.tsx:108` | `pending` | — | every skeleton + `data-scene-ready` (01) | EXCLUDED — store state; this *is* the readiness signal |
| `data/store.tsx:109` | `speed` | — | PrototypeStrip "Mock latency" | EXCLUDED — store state (spec names it, :168) |
| `data/store.tsx:120` | `handles` | — | which Volume Handle is open | EXCLUDED — store state (spec names it, :168) |
| `data/store.tsx:617` | `signedIn` | — | app ↔ `#/signin`/`#/setup` | EXCLUDED — mocked auth; the **route** selects the screen (`router.ts:117-118`). `useAuthMock` sits outside `PanelProvider`, so a `PanelProvider` prop cannot reach it (§5.6) |

| `pages/ComposePage.tsx:101` | `draft` | `compose.draft` | dirty banner, Discard/Save, editor contents (`:139`, `:293-311`) | CONVERT (clobbered on mount — §5.4) |
| `pages/ComposePage.tsx:102` | `fullscreenOpen` | `compose.fullscreenOpen` | `FullscreenEditor` (`:522-534`) | CONVERT (clobbered on mount — §5.4) |
| `pages/ComposePage.tsx:103` | `logsOpen` | `compose.logsOpen` | Logs `Drawer` (`:536-639`) | CONVERT — the spec's own example |
| `pages/ComposePage.tsx:104` | `logsText` | — | log body vs skeleton vs empty (`:612-631`) | EXCLUDED — data (`loadLogs`) |
| `pages/ComposePage.tsx:105` | `logsLoading` | `compose.logsLoading` | drawer skeleton (`:612-613`) | CONVERT |
| `pages/ComposePage.tsx:106` | `logServices` | `compose.logServices` | service filter (`:582-590`) | CONVERT (`string[]`; clobbered on mount — §5.4) |
| `pages/ComposePage.tsx:107` | `logLines` | `compose.logLines` | "Last N lines" `Select` (`:591-600`) | CONVERT |
| `pages/ComposePage.tsx:108` | `deleteOpen` | `compose.deleteOpen` | Delete Compose `DangerModal` (`:641-662`) | CONVERT |
| `pages/ProjectsPage.tsx:52` | `query` | `projects.query` | filter + "No Projects match" (`:155-171`) | CONVERT |
| `pages/ProjectsPage.tsx:53` | `sort` | `projects.sort` | Sort `Select` (`:138-147`) | CONVERT — **the spec's row is 1 short here** |
| `pages/ProjectsPage.tsx:54` | `view` | `projects.view` | Cards/Table `Segmented` (`:148-158`, `:175`) | CONVERT — the spec's own example |
| `pages/ProjectsPage.tsx:55` | `editing` | `projects.editing` | Edit modal title/prefill (`:316`) | **CONVERT\*** (`Project`) |
| `pages/ProjectsPage.tsx:56` | `modalOpen` | `projects.modalOpen` | New/Edit Project `Modal` (`:309-334`) | CONVERT |
| `pages/ProjectsPage.tsx:57` | `deleting` | `projects.deleting` | Delete Project `DangerModal` (`:336-356`) | **CONVERT\*** (`Project`) |
| `components/VariablesPanel.tsx:31` | `revealed` | `variables.revealed` | masked → revealed value cell (`:36`) | CONVERT — ⚠ one instance **per row** (§5.5) |
| `components/VariablesPanel.tsx:63` | `query` | `variables.query` | "No keys match …" (`:212`) | CONVERT |
| `components/VariablesPanel.tsx:64` | `editing` | `variables.editing` | Edit modal title/prefill (`:226`) | **CONVERT\*** (`EnvironmentVariable`) |
| `components/VariablesPanel.tsx:65` | `modalOpen` | `variables.modalOpen` | New/Edit Variable `Modal` (`:219-245`) | CONVERT |
| `components/VariablesPanel.tsx:66` | `bulkOpen` | `variables.bulkOpen` | paste-a-`.env` `Modal` (`:247-308`) | CONVERT |
| `components/VariablesPanel.tsx:67` | `bulkText` | `variables.bulkText` | preview, duplicate-key error, `okButtonProps` (`:75-76`, `:250`) | CONVERT — **the spec's row is 1 short here** |
| `pages/ProjectPage.tsx:68` | `environmentModalOpen` | `project.environmentModalOpen` | New Environment `Modal` (`:455-485`) | CONVERT |
| `pages/ProjectPage.tsx:69` | `composeModalOpen` | `project.composeModalOpen` | New Compose `Modal` (`:487-526`) | CONVERT |
| `pages/ProjectPage.tsx:70` | `composeYAML` | `project.composeYAML` | modal YamlEditor + `okButtonProps.disabled` (`:491`) | CONVERT — **the spec's row is 1 short here** |
| `pages/ProjectPage.tsx:71` | `deletingCompose` | `project.deletingCompose` | Delete Compose `DangerModal` (`:528-550`) | **CONVERT\*** (`Compose`) |
| `pages/ProjectPage.tsx:72` | `deleteProjectOpen` | `project.deleteProjectOpen` | Delete Project `DangerModal` (`:552-572`) | CONVERT |
| `components/ServiceMonitor.tsx:122` | `pickedService` | `monitor.service` | which series is charted (`:129`, `:176-195`) | CONVERT — spec's "series selection" |
| `components/ServiceMonitor.tsx:123` | `metricWindow` | `monitor.window` | window `Segmented` (`:197-201`) | CONVERT — spec's "window" |
| `components/ServiceMonitor.tsx:124` | `loading` | — | refresh spinner | EXCLUDED — data; store latency (spec's `4→2` is explicit) |
| `components/ServiceMonitor.tsx:125` | `result` | — | charts vs skeleton | EXCLUDED — data (`loadServiceMetrics`) |
| `app/AppShell.tsx:59` | `paletteOpen` | `shell.paletteOpen` | `CommandPalette` `Modal` (`:279`) | CONVERT |
| `app/AppShell.tsx:60` | `refreshing` | `shell.refreshing` | header Refresh spinner (`:242`) | CONVERT |

**The remaining 14 files — the spec's `28 → 20` row**

| file:line | state | proposed `useUiState` key | what it gates | verdict |
| --- | --- | --- | --- | --- |
| `pages/StatesGalleryPage.tsx:47` | `dangerOpen` | `states.dangerOpen` | "Delete this Compose?" `DangerModal` (`:609-627`) | CONVERT |
| `pages/StatesGalleryPage.tsx:48` | `confirmOpen` | `states.confirmOpen` | "Delete this Variable?" `DangerModal` (`:628-639`) | CONVERT |
| `pages/MonitoringPage.tsx:129` | `metricWindow` | `monitoring.window` | window `Segmented` (`:160-164`) | CONVERT |
| `pages/MonitoringPage.tsx:130` | `loading` | `monitoring.loading` | Refresh spinner (`:165`) | CONVERT — ⚠ inconsistent with `ServiceMonitor` (§5.3) |
| `pages/MonitoringPage.tsx:131` | `result` | `monitoring.result` | six charts vs six skeletons (`:181-193`) | **CONVERT\*** — ⚠ same shape as `ServiceMonitor`'s excluded pair |
| `pages/AuthPages.tsx:90` | `submitting` | `auth.submitting` | Sign-in button `loading` (`:123`) | CONVERT |
| `pages/AuthPages.tsx:140` | `step` | `auth.setupStep` | `Steps` + which form renders (`:148-154`) | CONVERT |
| `pages/OverviewPage.tsx:38` | `refreshing` | `overview.refreshing` | Refresh button `loading` (`:73`) | CONVERT |
| `components/MetricChart.tsx:175` | `width` | `chart.width` | SVG viewBox scale | **CONVERT\*** — ⚠ DOM-measured; a forced value is overwritten (§5.3) |
| `components/MetricChart.tsx:176` | `hoverAt` | `chart.hoverAt` | the crosshair/nearest-point readout (`:253-265`) | CONVERT — ⚠ pointer-transient, no rule excludes it |
| `components/ComposeVolumesTab.tsx:41` | `volumes` | `volumes.volumes` | table rows vs skeleton | **CONVERT\*** — ⚠ same shape as `VolumeExplorerPage`'s excluded `volumes` (§5.3) |
| `components/ComposeVolumesTab.tsx:42` | `error` | `volumes.error` | "Not resolvable until this Compose is deployed" (`:74-90`) | CONVERT |
| `components/ComposeVolumesTab.tsx:43` | `starting` | `volumes.starting` | `StartHandleDialog` for one row (`:255-272`) | **CONVERT\*** (`Volume`) |
| `components/FullscreenEditor.tsx:46` | `revealed` | `fullscreen.revealed` | masked → revealed in the rail (`:82`) | CONVERT — ⚠ one instance per variable row (§5.5) |
| `components/FullscreenEditor.tsx:172` | `query` | `fullscreen.railQuery` | rail filtered to nothing (`:174-178`) | CONVERT |
| `components/DangerModal.tsx:33` | `typed` | `danger.typed` | OK `disabled` — the type-the-name state (`:39`, `:45`) | CONVERT (clobbered on mount — §5.4) |
| `components/CommandPalette.tsx:27` | `query` | `palette.query` | hits / "No match for …" (`:120-124`) | CONVERT (clobbered on mount — §5.4) |
| `components/StartHandleDialog.tsx:43` | `mode` | `startHandle.mode` | rw/ro radio + the read-only warning (`:134`) | CONVERT (clobbered on mount — §5.4) |
| `components/StartHandleDialog.tsx:44` | `uid` | `startHandle.uid` | uid field + "manual" source tag (`:56`) | CONVERT (clobbered on mount — §5.4) |
| `components/StartHandleDialog.tsx:45` | `gid` | `startHandle.gid` | gid field | CONVERT (clobbered on mount — §5.4) |
| `pages/ScreensGalleryPage.tsx:202` | `frameHeight` | — | iframe height control (`:305`) | EXCLUDED — page deleted by 08 (ticket 02:13) |
| `pages/ScreensGalleryPage.tsx:203` | `notesOpen` | — | `Switch` (`:316`) | EXCLUDED — page deleted by 08 |
| `pages/ScreensGalleryPage.tsx:204` | `reloads` | — | iframe `key`, re-mount | EXCLUDED — page deleted by 08 |
| `pages/ScreensGalleryPage.tsx:205` | `zoom` | — | `Segmented` (`:280`) | EXCLUDED — page deleted by 08 |
| `main.tsx:72` | `mode` | — | light/dark theme | EXCLUDED — board-level theme switch, not a `ui` key (spec.md:424) |
| `app/useNow.ts:11` | `now` | — | every relative time + the idle countdown | EXCLUDED — clock, forced by `?clock=` (ticket 01) |
| `app/router.ts:129` | `route` | — | which screen renders | EXCLUDED — router state (spec.md:161) |
| `app/router.ts:140` | `hash` | — | `Menu` `selectedKeys` via `useActivePath` | EXCLUDED — router state |

**Where reality differs from the spec's table** — three rows, all under-counts, 59 vs the spec's 56 rows /
"≈55":

- `pages/ProjectsPage.tsx` — spec 6→5, actual 6→**6**. The extra is `sort` (`:53`, the Sort `Select`) or
  `editing` (`:55`); neither is derived, a ref or router state, so neither is excludable by the stated
  rule. `grep -c useState pages/ProjectsPage.tsx` = 7 (6 sites + 1 import).
- `components/VariablesPanel.tsx` — spec 6→5, actual 6→**6**. The extra is `bulkText` (`:67`), which is
  not a convenience: the duplicate-key error (`:76`) and the disabled OK (`:250`) *are* `bulkText`.
- `pages/ProjectPage.tsx` — spec 5→4, actual 5→**5**. The extra is `composeYAML` (`:70`), or one of the
  two object-valued modals.
- Everything else matches: the 13/8/8/6/4/2/2 per-file site counts and the 22-file, 80-site totals are
  exact; the `28 → 20` group row is exact (20 of 28, the 8 exclusions being `ScreensGallery` 4,
  `router` 2, `main` 1, `useNow` 1).

### 2. Overlay forcing table

antd `6.5.1` (`web-design/package.json:18`). **Every `Modal`, `Drawer`, `Dropdown`, `Popconfirm`,
`Select` and `Tooltip` in the mock accepts a controlled `open`** — none has to be circumvented, so
ticket 02's approach holds. The distinction that matters is whether the call site *already passes* it.

**Already controlled by state (18 mounted instances)** — forcing is a key rename, no JSX change:

| file:line (element) | mounted from | controlled prop | `ui` key |
| --- | --- | --- | --- |
| `pages/VolumeExplorerPage.tsx:575` `Modal` | same file | `open={Boolean(newEntry)}` (`:581`) | `volume.newEntry` |
| `pages/VolumeExplorerPage.tsx:788` `Modal` | `:598` | `open` prop (`:796`) | `volume.downloadOpen` |
| `components/StartHandleDialog.tsx:59` `Modal` | `VolumeExplorerPage.tsx:313` | `open` prop (`:67`) | `volume.startOpen` |
| `components/StartHandleDialog.tsx:59` `Modal` | `components/ComposeVolumesTab.tsx:256` | `open={Boolean(starting)}` (`:267`) | `volumes.starting` |
| `components/FullscreenEditor.tsx:197` `Modal` | `ComposePage.tsx:522` | `open={fullscreenOpen}` (`:531`) | `compose.fullscreenOpen` |
| `pages/ComposePage.tsx:536` `Drawer` | same file | `open={logsOpen}` (`:570`) | `compose.logsOpen` |
| `components/DangerModal.tsx:42` `Modal` | `ComposePage.tsx:641` | `open={deleteOpen}` (`:660`) | `compose.deleteOpen` |
| `components/DangerModal.tsx:42` `Modal` | `ProjectsPage.tsx:336` | `open={Boolean(deleting)}` (`:354`) | `projects.deleting` |
| `components/DangerModal.tsx:42` `Modal` | `ProjectPage.tsx:528` | `open={Boolean(deletingCompose)}` (`:548`) | `project.deletingCompose` |
| `components/DangerModal.tsx:42` `Modal` | `ProjectPage.tsx:552` | `open={deleteProjectOpen}` (`:570`) | `project.deleteProjectOpen` |
| `components/DangerModal.tsx:42` `Modal` | `StatesGalleryPage.tsx:609` | `open={dangerOpen}` (`:624`) | `states.dangerOpen` |
| `components/DangerModal.tsx:42` `Modal` | `StatesGalleryPage.tsx:628` | `open={confirmOpen}` (`:637`) | `states.confirmOpen` |
| `pages/ProjectsPage.tsx:309` `Modal` | same file | `open={modalOpen}` (`:315`) | `projects.modalOpen` |
| `pages/ProjectPage.tsx:455` `Modal` | same file | `open={environmentModalOpen}` (`:461`) | `project.environmentModalOpen` |
| `pages/ProjectPage.tsx:487` `Modal` | same file | `open={composeModalOpen}` (`:495`) | `project.composeModalOpen` |
| `components/VariablesPanel.tsx:219` `Modal` | same file | `open={modalOpen}` (`:225`) | `variables.modalOpen` |
| `components/VariablesPanel.tsx:247` `Modal` | same file | `open={bulkOpen}` (`:259`) | `variables.bulkOpen` |
| `components/CommandPalette.tsx:76` `Modal` | `app/AppShell.tsx:279` | `open={paletteOpen}` (`:80`) | `shell.paletteOpen` |

```
$ grep -rn 'open={\|open={' web-design/src --include=*.tsx | wc -l
23        # 18 call sites (the table above) + 5 `open` props inside the overlay components themselves:
          # VolumeExplorerPage.tsx:796, FullscreenEditor.tsx:202, DangerModal.tsx:49,
          # CommandPalette.tsx:80, StartHandleDialog.tsx:67
```

**Overlays that pass no `open` today** — all forceable, all need a call-site edit:

| file:line | element | today | note |
| --- | --- | --- | --- |
| `app/AppShell.tsx:258` | `Dropdown` (account) | `trigger` click | add `open`; key `shell.accountMenu` |
| `pages/ComposePage.tsx:240` | `Dropdown` (More) | `trigger` click | key `compose.moreOpen` — the spec's "More-menu open" scene |
| `pages/ProjectsPage.tsx:187`, `:291` | `Dropdown` (card + row) | `trigger` click | **one element per project** — a boolean opens every row at once (§5.5) |
| `pages/ProjectPage.tsx:310` | `Dropdown` (compose row) | `trigger` click | same, per compose |
| `pages/VolumeExplorerPage.tsx:720` | `Dropdown` (entry row) | `trigger` click | the spec's "row dropdown" scene — per entry |
| `pages/VolumeExplorerPage.tsx:737` | `Popconfirm` (delete entry) | uncontrolled | ⚠ has **no `onConfirm`** at all (§5.6) |
| `pages/ProjectPage.tsx:393` | `Popconfirm` (delete environment) | uncontrolled | per environment row; `onConfirm` at `:400` |
| `components/VariablesPanel.tsx:195` | `Popconfirm` (delete variable) | uncontrolled | the spec's "delete popconfirm" scene; per row |
| `pages/ComposePage.tsx:582` | `Select` (services, `mode="multiple"`) | `value={logServices}` | `open` is a *separate* key from the value |
| `pages/ComposePage.tsx:591` | `Select` (lines) | `value={logLines}` | same |
| `pages/ProjectsPage.tsx:138` | `Select` (sort) | `value={sort}` | same |
| `components/ServiceMonitor.tsx:176` | `Select` (service) | `value={service.name}` | same |

**Value-controlled (no `open` to force, the value *is* the state)**: `Segmented` —
`MonitoringPage.tsx:160`, `ServiceMonitor.tsx:197`, `ProjectsPage.tsx:148`, `ProjectPage.tsx:192`,
`AppShell.tsx:313`, `ScreensGalleryPage.tsx:280/294/305`; `Switch` — `AppShell.tsx:248`,
`ScreensGalleryPage.tsx:316`; `Radio.Group` — `VolumeExplorerPage.tsx:799`,
`StartHandleDialog.tsx:134`.

**Tooltips — 26 `<Tooltip` sites in 9 files**, none passing `open` (all hover-triggered):
`pages/VolumeExplorerPage.tsx` 4 (`:265`, `:284`, `:500`, `:677`), `pages/ComposePage.tsx` 5 (`:293`,
`:303`, `:348`, `:549`, `:601`), `pages/OverviewPage.tsx:269`, `app/AppShell.tsx` 2 (`:240`, `:245`),
`components/LiveHandlesBanner.tsx:85`, `components/ComposeVolumesTab.tsx` 2 (`:123`, `:138`),
`components/primitives.tsx` 5 (`:15`, `:35`, `:44`, `:63`, `:97`), `components/FullscreenEditor.tsx` 5
(`:82`, `:95`, `:113`, `:250`, `:258`), `components/VariablesPanel.tsx:38`.
(`grep -rn 'Tooltip' web-design/src --include=*.tsx | grep -c '<Tooltip'` → `26`.) No scene in the
spec's inventory names a tooltip, so these are 26 call-site edits nobody needs yet.

### 3. The two fictions

**3a. `after: 'toast:…'`** — every `message.*` / `notification.*` call site reachable from a screen:
**43 call sites across 10 files** (`grep -rn 'message\.\|notification\.' web-design/src --include=*.tsx
--include=*.ts | wc -l` → **44**: the 43 call sites plus one prose false positive at
`StatesGalleryPage.tsx:339`, where a sentence happens to end in the word "notification."). Copy is quoted
verbatim; `${…}` is the interpolated part.

| file:line | severity | copy | suggested `after` |
| --- | --- | --- | --- |
| `pages/ComposePage.tsx:171` | success | `Saved docker-compose.yml` | `toast:compose.saved` |
| `pages/ComposePage.tsx:179` | success | `Deployed ${compose.name}` | `toast:compose.deployed` |
| `pages/ComposePage.tsx:198` | success | `Restarted ${compose.name}` | `toast:compose.restarted` |
| `pages/ComposePage.tsx:205` | success | `Stopped ${compose.name}` | `toast:compose.stopped` |
| `pages/ComposePage.tsx:543` | success | `Copied visible log lines` | `toast:compose.logsCopied` |
| `pages/ComposePage.tsx:656` | success | `Deleted ${compose.name}` | `toast:compose.deleted` |
| `pages/ProjectsPage.tsx:90` | success | `Saved ${values.name}` — *the spec's "Saved my-saas"* (spec.md:174-175) | `toast:project.saved` |
| `pages/ProjectsPage.tsx:93` | success | `Created ${created.name}` | `toast:project.created` |
| `pages/ProjectsPage.tsx:97` | error | `Could not save the Project` | `toast:project.saveFailed` |
| `pages/ProjectsPage.tsx:106` | success | `Deleted ${name}` | `toast:project.deleted` |
| `pages/ProjectPage.tsx:111` | success | `Created Environment ${values.name}` | `toast:env.created` |
| `pages/ProjectPage.tsx:120` | success | `Created ${values.name}` | `toast:compose.created` |
| `pages/ProjectPage.tsx:128` | success | `Deployed \| Restarted \| Stopped ${compose.name}` (one call, three strings) | `toast:compose.lifecycle` |
| `pages/ProjectPage.tsx:349` | success | `Saved Project details` | `toast:project.detailsSaved` |
| `pages/ProjectPage.tsx:400` | success | `Deleted Environment ${environment.name}` | `toast:env.deleted` |
| `pages/ProjectPage.tsx:545` | success | `Deleted ${name}` | `toast:compose.deleted` |
| `pages/ProjectPage.tsx:566` | success | `Deleted ${project.name}` | `toast:project.deleted` |
| `pages/VolumeExplorerPage.tsx:197` | success | `Saved ${editing.path.split('/').pop()}` | `toast:volume.fileSaved` |
| `pages/VolumeExplorerPage.tsx:214` | success | `Uploaded ${file.name}` | `toast:volume.uploaded` |
| `pages/VolumeExplorerPage.tsx:231` | success | `Created ${joinPath(path, name)}` | `toast:volume.created` |
| `pages/VolumeExplorerPage.tsx:273` | success | `Closed ${volume?.declaredAs ?? 'the Volume'}` | `toast:volume.handleClosed` |
| `pages/VolumeExplorerPage.tsx:319` | success | `Opened ${volume?.declaredAs ?? 'the Volume'}` | `toast:volume.handleOpened` |
| `pages/VolumeExplorerPage.tsx:519` | warning | `${entry.name} points outside this Volume, so it is not followed.` | `toast:volume.symlinkRefused` |
| `pages/VolumeExplorerPage.tsx:729` | warning | `Downloading ${entry.name}` | `toast:volume.downloading` |
| `pages/VolumeExplorerPage.tsx:730` | warning | `Rename is out of scope for the design pass` | `toast:volume.renameOutOfScope` |
| `pages/VolumeExplorerPage.tsx:794` | success | `Downloading ${target}.${format}` | `toast:volume.archiveStarted` |
| `components/VariablesPanel.tsx:93` | success | `Saved ${values.key}` / `Added ${values.key}` (ternary on `editing`) | `toast:variable.saved` |
| `components/VariablesPanel.tsx:256` | success | `Replaced Environment Variables with ${parsed.length} keys` | `toast:variables.replaced` |
| `components/ComposeVolumesTab.tsx:79` | success | `Deploying…` (on a button labelled *Deploy this Compose*) | `toast:volumes.deploying` |
| `components/ComposeVolumesTab.tsx:201` | success | `Closed ${volume.declaredAs}` | `toast:volume.handleClosed` |
| `components/ComposeVolumesTab.tsx:263` | success | `Opened ${volume.declaredAs}` | `toast:volume.handleOpened` |
| `components/FullscreenEditor.tsx:102` | success | `Copied ${reference(variable.key)}` | `toast:editor.referenceCopied` |
| `components/LiveHandlesBanner.tsx:35` | success | `Closed ${name}` | `toast:volume.handleClosed` |
| `pages/AuthPages.tsx:160` | success | `Admin account created` | `toast:auth.adminCreated` |
| `pages/StatesGalleryPage.tsx:233` | success | `Submitted` (form `onFinish`) | `toast:states.submitted` |
| `pages/StatesGalleryPage.tsx:260` | success | `Deployed web` | `toast:states.deployed` |
| `pages/StatesGalleryPage.tsx:263` | error | `Could not stop worker: container is restarting` | `toast:states.error` |
| `pages/StatesGalleryPage.tsx:264` | info | `Status refreshed from the host` | `toast:states.info` |
| `pages/StatesGalleryPage.tsx:267` | warning | `Deploy is still running — the YAML you saved is not live yet` | `toast:states.warning` |
| `pages/StatesGalleryPage.tsx:276` → `:284` | notification `info` → `success`, keyed `deploy-demo`, 2.5 s apart | `Deploying my-saas / production / web` / `Deploy finished` (each with a `description`) | `toast:states.deployNotification` |
| `pages/StatesGalleryPage.tsx:622` | success | `Deleted web` | `toast:states.composeDeleted` |
| `pages/StatesGalleryPage.tsx:635` | success | `Deleted DATABASE_URL` | `toast:states.variableDeleted` |

Notes for the toast adapter:
- `message`/`notification` come from `App.useApp()`; the adapter has to run inside the `App` provider,
  i.e. on mount of the screen, not from `main.tsx` (only `StatesGalleryPage.tsx:45` draws `notification`).
- The spec's example copy (`spec.md:174-175`: `Saved my-saas / production / web`) is **not verbatim in the
  code**: the closest are `ProjectsPage.tsx:90` `Saved ${values.name}` and `ProjectPage.tsx:349`
  `Saved Project details`.
- Five call sites live inside a component that is mounted only when another key is forced —
  `ComposeVolumesTab.tsx:79/201/263`, `FullscreenEditor.tsx:102`, `LiveHandlesBanner.tsx:35` — so their
  `after` value has to imply the other key too.

**3b. `after: 'submit'`** — every form/modal whose post-submit state is worth a scene.
**19 surfaces on real screens, 3 in the states gallery.** "Adapter must invoke" is the real handler, not
a fake error object (ticket 02:21-23).

| file:line (surface) | submit handler | the state worth a scene | adapter must invoke |
| --- | --- | --- | --- |
| `pages/ProjectsPage.tsx:309` New/Edit Project `Modal` | `Form onFinish` `:318` → `submit` `:86` | `Enter a Project name` / `Use 255 characters or fewer` under the field (`:323-326`) | `form.submit()` with the name empty |
| `pages/ProjectsPage.tsx:336` `DangerModal` | `onConfirm` `:101` | the OK button is `disabled` until `typed === deleting.name` (`:39`, `:45`) — "type the name" | force `projects.deleting`, then `typed` |
| `pages/ProjectPage.tsx:455` New Environment `Modal` | `Form onFinish` `:464` → `:107` | `Enter an Environment name` (`:469`) | `form.submit()` empty |
| `pages/ProjectPage.tsx:487` New Compose `Modal` | `Form onFinish` `:499` → `:114` | required + `Use letters, digits, dashes and underscores` (`:503-506`); OK `disabled` until `composeYAML` (`:491`) | `form.submit()` with a bad name; force `project.composeYAML` for the enabled state |
| `pages/ProjectPage.tsx:345` Settings project form | `onFinish` `:348` | `Saved Project details` (`:349`) | the form's `onFinish` with valid values |
| `pages/ProjectPage.tsx:393` delete-environment `Popconfirm` | `onConfirm` `:400` | Environment disappears from the table | click/`onConfirm` for that row |
| `pages/ProjectPage.tsx:528` Delete Compose `DangerModal` | `onConfirm` `:540` | `Deleted ${name}` (`:545`) | force `project.deletingCompose` + typed name |
| `pages/ProjectPage.tsx:552` Delete Project `DangerModal` | `onConfirm` `:563` | `Deleted ${project.name}` then `navigate(paths.projects)` (`:566-567`) | same, with `deleteProjectOpen` |
| `components/VariablesPanel.tsx:219` New/Edit Variable `Modal` | `Form onFinish` `:228` → `:90` | `Enter a key` / `Use 255 characters or fewer` / `Use letters, digits and underscores only` (`:233-237`) | `form.submit()` empty or with `DATABASE-URL` |
| `components/VariablesPanel.tsx:247` paste-a-`.env` `Modal` | no `Form`; `onOk` `:253`, OK `disabled` while `parsed.length === 0 \|\| duplicateKeys.length > 0` (`:250`) | the duplicate-key refusal + the parsed preview (`:75-76`) | set `variables.bulkText` to the fixture `.env` — the "paste" *is* the state |
| `components/VariablesPanel.tsx:195` delete-variable `Popconfirm` | `onConfirm` `:200` | row removed (no toast) | click for that row |
| `pages/ComposePage.tsx:641` Delete Compose `DangerModal` | `onConfirm` `:653` | `Deleted ${compose.name}` + `navigate(environment)` (`:656-657`) | force `compose.deleteOpen` + typed name |
| `pages/ComposePage.tsx:522` `FullscreenEditor` | `onSave` `:530` → `save()` `:169` | `Saved docker-compose.yml` (`:171`); dirty flag clears (`:139`) | force `compose.draft` ≠ `yamlContent`, then save |
| `pages/VolumeExplorerPage.tsx:575` New file/folder `Modal` | `onOk` `createEntry` `:217` | OK `disabled` until the name is non-empty (`:577`); `New folder` vs `New file` title (`:582`) | force `volume.newEntry`, then `newEntryName` |
| `pages/VolumeExplorerPage.tsx:788` DownloadDialog `Modal` | `onOk` `:792` | `Downloading ${target}.${format}` + modal closes (`:794`) | force `volume.downloadOpen` + `archiveFormat`, then OK |
| `pages/VolumeExplorerPage.tsx:737` delete-entry `Popconfirm` | **none** | — | nothing to invoke: no `onConfirm` exists (§5.6) |
| `components/StartHandleDialog.tsx:59` Open Volume `Modal` | `onOk` `:66` → `onStart` | OK `disabled` when `volume.existence === 'absent'` (`:55`, `:63`) — the absent-path refusal | force the absent Volume fixture + `volume.startOpen` |
| `pages/AuthPages.tsx:105` Sign-in form | `onFinish` `:108` | `submitting` (`:90`) is set *inside* `onFinish`, then `onSignIn()` after 500 ms; validation `:117-120` | `form.submit()` with valid values (submitting) / empty (errors) |
| `pages/AuthPages.tsx:155` Setup form | `onFinish` `:158` → `setStep(1)` + toast `:160` | `Enter your email address`, `Enter a valid email address`, `Use at least 8 characters`, `Confirm your password`, **`Passwords do not match`** (`:167-198`) | `form.submit()` with a mismatched confirm — the only genuinely unreachable state today |

The states gallery adds 3 more surfaces of its own (`StatesGalleryPage.tsx:233` form,
`:609` and `:628` `DangerModal`s) — those are demo copy already pre-painted
(`validateStatus="error"` at `:242`), so they need no adapter.

**Counts:** 43 toast call sites, 22 submit surfaces (19 on real screens). The spec's inventory names a
small subset — "form validation", "outcome toasts and a keyed notification", "destructive confirmations"
(`spec.md:275-281`) — which the 6 gallery outcomes plus roughly 8 submit adapters cover.

### 4. Mobile keys ticket 03 will need (named, not implemented)

From spec.md:294-341. Only the navigational states need a `ui` key; everything else 03 changes is
viewport-driven and is a registry `viewports` concern (spec.md:329-331), not a key:

- `shell.navOpen` — the sider re-hosted as a `Drawer` at tablet and mobile widths, with a top-bar menu
  button (spec.md:312-313: "`shell-nav` is a scene, because 'the menu on a phone' is itself a state worth
  reviewing"). Today the sider has no state at all: `AppShell.tsx:158-163` is `Layout.Sider
  breakpoint="lg" collapsedWidth={0}`.
- `shell.overflowOpen` — spec.md:315's header cluster collapsing "to icons plus an overflow menu".
- `shell.accountMenu` — already required by **this** ticket for the desktop account `Dropdown`
  (`AppShell.tsx:258`, §2); 03 reuses the same key rather than adding one.
- `shell.navGroup` — only if the drawer reproduces the menu's groups expanded (`AppShell.tsx:73-88`,
  `:90-98`); the spec does not name it, so it is an "if 03 needs it" key.
- Explicitly **not** keys: full-height sheet modals, stacked tables, `Popconfirm` → dialog, the Compose
  rail stacking, the full-screen editor's rail, `Segmented`/`Tabs` scrolling (spec.md:316-326). All are
  widths, not states.

Mobile nav has no state to convert today, so ticket 03 adds `shell.navOpen` from scratch on top of the
hook — nothing in this ticket has to anticipate it (ticket 02:28-30).

### 5. Anything that looks wrong (recorded, not fixed)

**5.1 The spec's own example key is not a `useState` site.** `?ui=compose.tab=logs` (spec.md:136) — the
`Tabs` are `activeKey={activeTab}` (`ComposePage.tsx:269`) derived from the route prop (`:78-82`), parsed
at `router.ts:14`, `:104-106`. The same holds for `ProjectPage.tsx:214` (`tab` from `router.ts:13`,
`:106`) and the environment `Segmented` (`ProjectPage.tsx:192-193`, which calls `goToEnvironment` →
`navigate`). So `uiKeys.ts` will hold a key with no inventory row. Decide whether that is allowed, or
whether `compose.tab` should not be a `ui` key at all.

**5.2 Nine converts hold object/array values, which a `?ui=` string cannot produce.** Ticket 01 says
values are coerced from strings — `1`/`0`/`true`/`false`, else the string
(`01-scene-harness.md`). These nine need either a typed override map (the adapter supplies the object) or
a scalar companion key: `VolumeExplorerPage.tsx:108` `editing`, `:109` `editorTooLarge`;
`ProjectsPage.tsx:55` `editing`, `:57` `deleting`; `VariablesPanel.tsx:64` `editing`;
`ProjectPage.tsx:71` `deletingCompose`; `ComposeVolumesTab.tsx:41` `volumes`, `:43` `starting`;
`MonitoringPage.tsx:131` `result`. If the rule is "URL-forceable only", the count is **50**, not 59.

**5.3 Three inconsistencies in what counts as data.** Same shape, opposite verdicts:
- `MonitoringPage.tsx:130-131` (`loading`, `result`) — CONVERT as the spec's `28 → 20` row requires,
  while `ServiceMonitor.tsx:124-125` (identical shape, same charts) — EXCLUDED by the spec's explicit
  `4 → 2`. Both fetch from the same store through the same latency.
- `ComposeVolumesTab.tsx:41` `volumes` — CONVERT, while `VolumeExplorerPage.tsx:103`/`:105`
  (`volumes`, `entries`, the same `resolveComposeVolumes`/`listDir` data) — EXCLUDED.
- `MetricChart.tsx:175` `width` — CONVERT as the row arithmetic requires, but the value is **measured**:
  a `useLayoutEffect` runs on *every* render (`:182-187`) and a `ResizeObserver` keeps correcting it
  (`:189-198`), so a forced `chart.width` is overwritten immediately and cannot stick.

**5.4 Eight converted sites are clobbered on mount by an existing effect** — `useUiState` alone will
**not** force them, so they need a code change (this is the one place ticket 02 is more than a swap):

| site | effect that erases the forced value |
| --- | --- |
| `ComposePage.tsx:101` `draft` | `:110-112` `setDraft(compose?.yamlContent ?? '')`, deps `[compose?.id, compose?.yamlContent]`, runs on mount |
| `ComposePage.tsx:106` `logServices` | `:114-117` `setLogServices([])`, deps `[composeId]` |
| `ComposePage.tsx:102` `fullscreenOpen` | `:120-122` `setFullscreenOpen(false)`, deps `[composeId]` |
| `components/StartHandleDialog.tsx:43/44/45` `mode`/`uid`/`gid` | `:47-53` resets all three when `open` is true — a forced `mode=ro` is back to `'rw'` on mount |
| `components/CommandPalette.tsx:27` `query` | `:29-31` `if (open) setQuery('')` |
| `components/DangerModal.tsx:33` `typed` | `:35-37` `if (open) setTyped('')` |
| `pages/VolumeExplorerPage.tsx:163` `expiredHere` | `:165-172` `setExpiredHere(false)` when a handle is open |
| `pages/MonitoringPage.tsx:130/131` `loading`/`result` | `:145-147` `void load(metricWindow)` on mount; same at `ServiceMonitor.tsx:144-146` |

Each wants the reset keyed on a *transition* (the previous value of `open`) rather than on
`open === true`, or the override has to be applied after mount effects.

**5.5 Six state sites are per-row, so one boolean key would affect every row at once.**
`VariablesPanel.tsx:31` and `FullscreenEditor.tsx:46` (`revealed` — one `ValueCell`/`VariableRow` per
variable) and the row overlays `VolumeExplorerPage.tsx:720`/`:737`, `ProjectsPage.tsx:187`/`:291`,
`ProjectPage.tsx:310`, `VariablesPanel.tsx:195`. A scene called "one row revealed" needs an addressed
value (`variables.revealed=DATABASE_URL`) or it has to mean "all rows revealed".

**5.6 Two dead-confirm findings.** `VolumeExplorerPage.tsx:737-751`: the delete-entry `Popconfirm` has
**no `onConfirm` at all** (`grep -n 'onConfirm' web-design/src/pages/VolumeExplorerPage.tsx` → no
output), so confirming does nothing and no toast fires; the spec's "delete popconfirm" scene
(`spec.md:273`) would photograph a confirm that is a no-op, and §3b has no real transition to invoke
there. Also `StatesGalleryPage.tsx:312-316` labels a button *"Delete a Variable (Popconfirm)"* but
`onClick={() => setConfirmOpen(true)}` opens a `DangerModal` (`:628`) — there is no `<Popconfirm>`
element in that file at all (`grep -rn '<Popconfirm' web-design/src` → 3 sites: `VolumeExplorerPage.tsx:737`,
`ProjectPage.tsx:393`, `VariablesPanel.tsx:195`). The gallery therefore shows no real `Popconfirm`, which
matters because ticket 02's checklist item 3 promises controlled `Popconfirm` forcing.

**5.7 `signedIn` is out of `SceneOverrides`' reach.** `data/store.tsx:617` lives in `useAuthMock`, called
from `main.tsx:74` in `Root` — outside `PanelProvider` (`data/store.tsx:103`), so a `PanelProvider` prop
cannot seed it. The route (`#/signin`, `#/setup`) is the mechanism that exists; that is consistent with
the spec's `0` for `store.tsx`, but it means "signed out" is reachable only as a route, not as a key.

**5.8 `useNow.ts:11` ticks once a second** (`:14` `setInterval`). Two visible consequences for
determinism: `VolumeExplorerPage.tsx:266`'s "closes in N min" and the seeded handle's
`lastUsedAt`/`startedAt` (`data/store.tsx:121-131`). Ticket 01's `?clock=` is the fix; noted because the
countdown is the one thing in this mock that re-renders on a timer (`useNow.ts:5-8` says so).

**5.9 `ScreensGalleryPage` still renders today.** Its four sites are excluded per ticket 02:13, but
`zoom` (`:205`), `notesOpen` (`:203`), `frameHeight` (`:202`) and `reloads` (`:204`) are the affordances
the board reintroduces (spec.md:379-423). Recorded so 04/08 does not rediscover them; not to be converted
here.

**5.10 Two "draft content" converts want a named fixture, not a literal key.**
`ProjectPage.tsx:70` `composeYAML` and `ComposePage.tsx:101` `draft` are whole YAML documents; a `?ui=`
value carrying a file is not viable, but both have a natural fixture — `ProjectPage.tsx:513`
`setComposeYAML(STARTER_YAML)` and `ComposePage.tsx:529` `setDraft(compose.yamlContent)`.

### Uncertain

- **Whether the nine `CONVERT*` object/array sites belong in the count.** The spec counts object-valued
  state as converting (that is the only reading that makes its `28 → 20` row add up), but ticket 01
  specifies string coercion for `?ui=` values. 59 if they count, 50 if only URL-scalars count. I could
  not settle this from the two documents; it is a decision ticket 01/02 has to make.
- **Which site each of the three under-counted rows meant to exclude.** I named the plausible candidate
  per file (`sort` or `editing`; `bulkText` or `revealed`; `composeYAML` or a delete-object) but the spec
  does not say, and each choice moves the total.
- **`MetricChart.tsx:176` `hoverAt`.** No stated rule excludes it (it is neither derived, a ref, nor
  router state) and no scene in the inventory names a hovered chart, so it is counted CONVERT above. If a
  reviewer's intent is "pointer-transient states are not scenes", it is the 51st exclusion.
- **`MonitoringPage.tsx:130-131` / `MetricChart.tsx:175`.** They are counted CONVERT only because the
  spec's `28 → 20` row requires 20; both look like accidents of that arithmetic rather than decisions
  (§5.3). If they are excluded, the group is 17 and the total 56.
- **Whether the low-value scalar sites are wanted at all:** `DangerModal.tsx:33` `typed`,
  `CommandPalette.tsx:27` `query`, `VolumeExplorerPage.tsx:113` `newEntryName`,
  `ProjectPage.tsx:70` `composeYAML`, `VariablesPanel.tsx:67` `bulkText`. They gate a rendered thing
  (usually an `okButtonProps.disabled`), so the stated rule converts them; a stricter rule ("a key only
  where a *frame* differs") would not.
- **Ticket 02's `**Status:**` line is still `needs-triage`** while ticket 01's Comments describe this
  audit and the ticket's own body reads as work-ready. Left untouched per instruction; flagged for the
  coordinator, not changed here.
- **Nothing was modified.** The only write is this Comments section. `git status --short` after writing
  should show `.scratch/design-board/issues/01-scene-harness.md` (ticket 01's own edit, not mine) plus
  this file.



- 2026-09-26 — **Coordinator: claimed for implementation, and the five questions the audit raised are
  answered here so no agent has to guess.** `Status:` moved to `ready-for-agent`. The audit's §5 findings
  are a change to the plan, not a footnote, so these rules are binding for every batch of this ticket.
  - **D1 — `?ui=` stays scalar-only.** Ticket 01's coercion (`1`/`0`/`true`/`false`, else the string) is
    not extended. The nine object/array sites in §5.2 are never forced by a URL literal: either a named
    `after:` adapter in `app/sceneAdapters.ts` seeds the object through the app's own setter, or the site
    is excluded and the reason recorded. No typed-override map, no second coercion path.
  - **D2 — per-row state is addressed by naming the row in the key** (§5.5). `?ui=variables.revealed.DATABASE_URL=1`
    parses with the existing "split on the first `=`" rule — no harness change. `uiKeys.ts` gains one
    thing: a declared key **family** with a trailing `.*` (`variables.revealed.*`, `volume.rowMenu.*`, …),
    so a legitimate addressed key does not warn while a key matching no declared family still does.
  - **D3 — a site clobbered on mount (§5.4) is fixed by re-keying the reset on the `open` transition**
    (false → true), not on `open === true`. For a reviewer with no query string that is
    behaviour-identical — the state starts at its default and only a click opens the overlay — and it
    lets a forced value survive a cold load. Where the effect is not an overlay reset at all
    (`MonitoringPage`/`ServiceMonitor`'s `load()` on mount, `ComposePage.tsx:110-112` `setDraft`), the
    effect keeps running exactly as it does today and that site's state is delivered by an `after:`
    adapter instead. No other behaviour moves.
  - **D4 — the acceptance list is the audit's 59-row CONVERT table, not "≈55".** The spec's own rows sum
    to 56 and three rows are one site short each; the audit's §1 table supersedes them. The final
    reconciliation in this ticket's Comments must show `80 = converted + excluded-by-rule + adapter-forced`
    with the rule named per site. §5.3's twins are aligned rather than argued: `MetricChart.tsx:175`
    `width` is excluded (measured — a layout effect and a `ResizeObserver` overwrite any forced value) and
    `MonitoringPage.tsx:130-131` follows the same verdict as its `ServiceMonitor.tsx:124-125` twin
    (excluded as store-fetched data; the loading *scene* is an `after:` adapter).
  - **D5 — `compose.tab` (§5.1) is not a `ui` key**, because the `Tabs` are the route
    (`router.ts:104-106`); the same holds for `ProjectPage`'s tab and environment `Segmented`. The spec's
    example at `spec.md:136` is corrected in ticket 09's docs pass, not here. `signedIn` (§5.7) stays
    route-only — `useAuthMock` is outside `PanelProvider`, so `SceneOverrides` cannot reach it; recorded,
    not worked around.
- 2026-09-26 — **Batches, so three agents can work this ticket without colliding.** Batch 1 (running
  now) owns the pattern and these files: `app/AppShell.tsx`, `pages/OverviewPage.tsx`,
  `pages/MonitoringPage.tsx`, `components/ServiceMonitor.tsx`, `components/StartHandleDialog.tsx`,
  `components/ComposeVolumesTab.tsx`, `components/CommandPalette.tsx`, `components/DangerModal.tsx`,
  `components/MetricChart.tsx`, `components/FullscreenEditor.tsx`, `pages/AuthPages.tsx`,
  `pages/StatesGalleryPage.tsx`, plus `app/sceneAdapters.ts` and the `uiKeys.ts` additions. It also fixes
  the **adapter registry's shape** — a registration API (`registerSceneAdapter(name, fn)`) so a later
  batch registers its own adapters from its own screen file and never edits the registry — because that
  is what keeps batches 2 and 3 parallel instead of serial. Reserved: batch 2 = `ProjectsPage`,
  `ProjectPage`, `VariablesPanel`; batch 3 = `ComposePage`, `VolumeExplorerPage`.
- 2026-09-27 — **Batch 1 landed.** 21 keys registered (`uiKeys.ts`), D3 re-keys applied
  (StartHandleDialog / CommandPalette / DangerModal now reset on the false→true transition), D4's
  exclusions applied (`MetricChart.width`, `MonitoringPage.loading/result`), `chart.hoverAt`
  converted, `states.section` added for ticket 04's feedback scenes. Two seams were added to
  `sceneAdapters.ts` so module-scope adapters can reach the running app: `<SceneAdapterBridge />`
  (captures the mounting AntApp's message/notification) and `publishSceneHandle(name, value)`
  (screens publish forms/setter recipes, auto-unpublished on unmount). Registered: five
  `toast:states.*`, the keyed deploy notification, `toast:auth.adminCreated`, three
  `submit:auth.*` adapters, `object:volumes.starting`. **D4 reconciliation, batch-1 files:**
  converted 17 sites / excluded 4 by rule (`MonitoringPage loading+result`, `ServiceMonitor
  loading+result` already excluded before this batch, `MetricChart.width`) / 1 excluded as data
  (`ComposeVolumesTab.volumes` — deviation from the audit's CONVERT*, recorded per D4's
  reconciliation rule). Known gaps carried for later batches/integration: `shell.accountMenu`
  (the account Dropdown needs a controlled `open` — AppShell call-site edit, not a swap),
  `overview-all-clear` needs a store lever (see ticket 04's Comments), and standalone `?after=`
  has no runner yet (only the scope's AfterRunner runs adapters; `main.tsx` was out of batch
  scope) — Open-live links to `after:` scenes stay unreproducible until the coordinator adds the
  standalone runner.
- 2026-09-27 — **Coordinator handover.** The previous session claimed this ticket and stopped before
  batch 1 produced code (no `sceneAdapters.ts`, no `useUiState` call sites; working tree clean). This
  session takes the claim over, running all three batches as specified above — batch 1 first for the
  pattern, batches 2 and 3 after it lands. D1–D5 above are binding as written. One amendment from the
  architecture update (spec.md, 2026-09-27): the board renders frames **live**, so the audit's
  "captured mid-toast" framing becomes "shown mid-toast" — the `duration: 0` mechanics are unchanged.
  Agents do not commit; the coordinator commits per batch.
- 2026-09-27 — **Batch-1 integration gaps closed** (gaps agent, coordinator session; ran alongside
  batches 2/3 and touched only the integration set: `app/AppShell.tsx`, `pages/OverviewPage.tsx`,
  `pages/MonitoringPage.tsx`, `components/ServiceMonitor.tsx`, `main.tsx`, plus append-only additions
  to `uiKeys.ts` / `sceneAdapters.ts`; `app/scene.ts` needed no change — `sceneAfter()` was already
  the seam). The four gaps from the batch-1 record:
  1. **`shell.accountMenu`.** The account `Dropdown` is now controlled — `open` + `onOpenChange`
     under `useUiState` (audit §2's "needs a call-site edit, not a swap", done). A click still opens
     and closes it; `?ui=shell.accountMenu=1` holds it open. Key declared.
  2. **`overview-all-clear` — decision: a screen key, not a store lever.** The store lever would have
     extended `SceneOverrides` + `PanelProvider`, both in `data/store.tsx`, which is outside this
     agent's file set while batches 2/3 share the tree. The declared fiction is
     `overview.attentionClear` (`?ui=overview.attentionClear=1`): the *derivations* consult it
     instead of computing — OverviewPage's attention list **and AppShell's refresh badge**, which
     counts the same Composes, so the frame cannot contradict itself (page says "All clear" while
     the badge still says 2). Registered `seed:overview.allClear`, which drives both derivations
     through their own setters (handles `overview.attention.seed.page` / `overview.attention.seed.shell`)
     — so **ticket 04's `overview-all-clear` scene needs no registry edit**; its `after:` name works
     as declared, and `?ui=` is the direct spelling if 04's review prefers it
     (`ui: { 'overview.attentionClear': 1 }`). This answers 04 open question (a) for this scene;
     `projects-empty` / `project-zero-envs` / `volume-explorer-readonly` stay with their batch owners.
  3. **`monitoring:loading` / `monitor:loading` — a held skeleton *is* reachable** (answers 04 open
     question (b)). D4 keeps `loading`/`result` as data and the mount `load()` running unchanged, so
     driving the setter alone cannot hold: the in-flight fetch's `finally` drops the skeleton when
     the mock answers. The hold is a published recipe that pins a ref the load's `finally` respects
     (`if (!hold) setLoading(false)`) plus `setLoading(true)` — the fetch still answers into
     `result`; only the loading state is pinned. Without an `after` name the ref is false and the
     code path is today's exactly. Adapters registered; handles `monitoring.loading.hold` /
     `monitor.loading.hold`.
  4. **Standalone `?after=` runner.** `<StandaloneAfterRunner />` inside the root `<AntApp>` in
     `main.tsx`, with `<SceneAdapterBridge />` beside it so standalone toast adapters reach the
     app's own antd handles. One run per mount (ref guard, StrictMode-safe): reads `sceneAfter()`,
     runs `getSceneAdapter(name)`; an unknown name is dev-loud — `getSceneAdapter`'s throw is caught
     and `console.error`ed, since standalone has no frame error boundary to crash into.
  **Ordering finding for every `publishSceneHandle` user (batch-1 files — reported, not fixed).**
  The scoped AfterRunner is the *first sibling* before `{children}` in `SceneScopeProvider`, and
  React flushes passive effects depth-first in tree order — so a handle published in a plain
  `useEffect` is not in the map yet when a board-driven adapter reads it (dev-warn, then a no-op).
  `AuthPages`' two form handles and `volumes.starting.seed` are `useEffect`-published; their board
  frames plausibly render the plain screen today. The one-line move — the one this pass used for all
  four new publishes — is `useLayoutEffect`: all layout effects flush before any passive effect, so
  sibling order stops mattering. The standalone runner is additionally placed after `PanelProvider`
  in the tree. (Reasoned from React's effect semantics; this environment has no browser to run a
  frame in — worth one board glance on an auth `after:` scene.)
  **Verification:** `pnpm --dir web-design typecheck` green; `vite build` green;
  `node --test scripts/scene.test.mjs` 13 pass; registry 16 pass / 1 skip plain, and
  `REGISTRY_ASSERT_KEYS=1` no longer names `shell.accountMenu`, `monitoring:loading`,
  `monitor:loading` or `seed:overview.allClear` — every remaining violation is a batch-2/batch-3
  name, exactly the list in ticket 04's Comments.


- 2026-09-27 — **Batch 2 landed** (`pages/ProjectsPage.tsx`, `pages/ProjectPage.tsx`,
  `components/VariablesPanel.tsx`, plus the appended `uiKeys.ts`/`sceneAdapters.ts` blocks). Per-site
  swap table (audit §1 line numbers; every site CONVERT, none excluded — the audit's per-file counts
  hold exactly):

  | file:line | state | key / mechanism |
  | --- | --- | --- |
  | ProjectsPage :53 | `query` | `projects.query` |
  | ProjectsPage :54 | `sort` | `projects.sort` |
  | ProjectsPage :55 | `view` | `projects.view` |
  | ProjectsPage :56 | `editing` | `projects.editing` — CONVERT\*: `object:projects.editing` seeds through the screen's own `openEdit` (prefill + open) |
  | ProjectsPage :57 | `modalOpen` | `projects.modalOpen` |
  | ProjectsPage :58 | `deleting` | `projects.deleting` — CONVERT\*: `object:projects.deleting`; `submit:projects.delete.confirmed` runs the real `onConfirm` |
  | ProjectPage :69 | `environmentModalOpen` | `project.environmentModalOpen` |
  | ProjectPage :70 | `composeModalOpen` | `project.composeModalOpen` |
  | ProjectPage :71 | `composeYAML` | `project.composeYAML` — §5.10 document; `fixture:project.composeYAML` seeds `STARTER_YAML` via the screen's own setter |
  | ProjectPage :72 | `deletingCompose` | `project.deletingCompose` — CONVERT\*: `object:project.deletingCompose` (seeded with web); `submit:project.deleteCompose.confirmed` |
  | ProjectPage :73 | `deleteProjectOpen` | `project.deleteProjectOpen` |
  | VariablesPanel :31 | `revealed` | `variables.revealed.<KEY>` — per-row D2, in `ValueCell` |
  | VariablesPanel :64 | `query` | `variables.query` |
  | VariablesPanel :65 | `editing` | `variables.editing` — CONVERT\*: `object:variables.editing` via `openEdit` (DATABASE_URL) |
  | VariablesPanel :66 | `modalOpen` | `variables.modalOpen` |
  | VariablesPanel :67 | `bulkOpen` | `variables.bulkOpen` |
  | VariablesPanel :68 | `bulkText` | `variables.bulkText` — §5.10 document; `fixture:variables.bulkText` / `fixture:variables.bulkDuplicate` seed a fixture `.env` (lives beside `parseDotEnv`) |

  Audit §2's uncontrolled per-row overlays got their D2 families as the registry scenes need:
  `projects.rowMenu.<name>` (card + table ⋯ menu, new `ProjectRowMenu`), `project.rowMenu.<compose>`
  (`ComposeRowMenu`), `project.deleteEnv.<env>` and `variables.deletePopconfirm.<key>` (the two delete
  `Popconfirm`s, re-hosted as `DeleteEnvironmentButton`/`DeleteVariableButton` with controlled `open` +
  `onOpenChange`). With no query string every one starts closed and clicks exactly as before.

- 2026-09-27 — **Batch 2's store-lever decision (ticket 04 open question a): adapters, not
  `SceneOverrides`.** The registry as written names `after: 'projects:empty'` and
  `after: 'seed:project.zeroEnvs'`, and both captions say "reached through an adapter" — a store lever
  would have required a registry `after`→`ui` swap, and `src/scenes/**` is not batch 2's to edit. Both
  scenes act through the store's own mutations (`projects.empty.seed` deletes every seeded Project;
  `project.zeroEnvs.seed` deletes project 4/"blog"'s one Environment — the scene hashes
  `paths.project(4)`, which exists), so readiness genuinely waits for the transitions.
  `src/data/store.tsx` is untouched. One deviation from ticket 04's Comments: it listed
  `seed:projects.empty`, but the registry scene carries `projects:empty` — the registry's name is what
  got registered.

- 2026-09-27 — **D3's reset fix completed in `DangerModal` and `CommandPalette`** (batch-1 files, one
  line each, the StartHandleDialog pattern): the `wasOpen` ref is now seeded from the mount-time
  `open`, so a load that forces `open` *and* the value — `project-delete-modal`
  (`project.deleteProjectOpen` + `danger.typed`) and `overview-palette-query` (`shell.paletteOpen` +
  `palette.query`) — no longer counts the mount as an opening and wiping the forced value. With no
  query string nothing moves; an adapter-seeded `open` (a real false→true transition) still resets.

- 2026-09-27 — **Batch 2's `submit:`/`after:` adapters** (16 registered): the registry's ten
  (`submit:projects.new.empty`, `object:projects.editing`, `object:projects.deleting`,
  `object:project.deletingCompose`, `object:variables.editing`, `fixture:project.composeYAML`,
  `fixture:variables.bulkText`, `fixture:variables.bulkDuplicate`, `projects:empty`,
  `seed:project.zeroEnvs`) plus the audit §3b rows no scene names yet (`submit:project.environment.empty`,
  `submit:project.compose.badName`, `submit:project.settings.saved`) and the typed-name flows
  (`submit:projects.delete.confirmed`, `submit:project.deleteCompose.confirmed`). All publish-handles
  use `useLayoutEffect`, per the integration pass's ordering finding above.
  **Deliberately not registered: `submit:project.deleteProject.confirmed`.** Its real handler
  navigates to `paths.projects` from inside a `.then()` — outside the AfterRunner's synchronous
  `scopeNavigate` claim (`router.ts:159-181`) — so in a frame the navigation would land on the window
  hash (or a sibling frame's claim) instead of the frame. That needs either a scope seam for async
  navigates or a registry decision before a scene names it; the `object:project.deletingCompose`-style
  seed (modal open, OK disabled) and `project.deleteProjectOpen` + `danger.typed` ui keys remain the
  reachable spellings of that flow.

- 2026-09-27 — **D4 reconciliation, batch-2 files:** 80 total = 17 converted here (ProjectsPage 6,
  ProjectPage 5, VariablesPanel 6) + excluded-by-rule 0 + adapter-forced 4 (`projects.editing`,
  `projects.deleting`, `project.deletingCompose`, `variables.editing` are converted sites whose
  forcing is adapter-only per D1; `project.composeYAML`/`variables.bulkText` are the §5.10 fixtures).
  **Verification:** `pnpm --dir web-design typecheck` green for every batch-2 file (the one error in
  the tree, `src/pages/ComposePage.tsx(88,5) TS2339: Property 'holdVolumeResolution' does not exist on
  type 'PanelValue'`, is batch 3's in-flight edit — reported, not touched); `node --test
  scripts/scene.test.mjs` 13 pass / 0 fail; `REGISTRY_ASSERT_KEYS=1 node --test
  scripts/registry.test.mjs` — every remaining violation is a batch-3 `compose.*`/`volume.*` name (29
  scene rows); no `projects.*`, `project.*` or `variables.*` name remains.
- 2026-09-27 — **All three batches + the gaps pass landed** (commits 05700d7, 827e4a0). Every one of the audit's 59 CONVERT rows is now a declared key or a named adapter; `REGISTRY_ASSERT_KEYS=1` runs clean (17/17), so every scene name in the registry is backed by the mock. Scene-handle publishes moved to `useLayoutEffect` (batch 3's finding: the scope's AfterRunner runs before passive effects, so `useEffect`-published handles missed board-driven adapters). The standalone `?after=` runner now exists in `main.tsx`, so *Open live* reproduces adapter states. Known and recorded, not fixed: a forced `volumes.error` is replaced when real resolution lands (the unresolvable scene mounts compose 3, whose error is genuine); `auth.submitting` lasts the app's real 500 ms. The per-key CDP sweep was covered by the scope isolation run (26 assertions), the board smoke (21) and the acceptance run (8), sampling every batch — not one URL per key.
