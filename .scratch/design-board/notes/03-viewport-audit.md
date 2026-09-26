# 03 — Viewport audit: what the mock actually does at 834 and 390

Read-only pass over `web-design/` (plus `GUIDELINES.md`, `README.md`, `.scratch/design-port/`). No source
file touched, nothing committed. Every row carries `file:line`.

**Method.** Text search + full reads of `src/`; counts are `grep -c`, not estimates. Line numbers are the
working tree at the time of reading, on branch `feat-design-board`, with ticket 02's `useUiState` swap in
flight — the spec cites `AppShell.tsx:159` for the Sider's `breakpoint`, which is `:159` for the prop and
`:158` for the element today. Treat every citation as ±1 in `AppShell.tsx` and in `02`'s converted screens.

**The one-sentence status quo.** The mock has **one** responsive prop, and it removes the navigation.

---

## 1. Current responsive status quo

### 1.1 Breakpoints and responsive props — the complete list

| what | file:line | value | effective floor |
| --- | --- | --- | --- |
| `Layout.Sider breakpoint` | `app/AppShell.tsx:158-161` (`:159` = `breakpoint="lg"`) | `"lg"` | **992px** |
| `Layout.Sider collapsedWidth` | `app/AppShell.tsx:160` | `0` | below 992 the sider has **zero** width and **no replacement** |
| `Col` / grid props | 36 sites, 7 files — §1.2 | `xs/sm/md/lg` | `lg` = 992 |
| `Descriptions column` (responsive object) | `components/ServiceMonitor.tsx:221-222` | `{ lg: 3, sm: 2, xs: 1 }` | 992 / 576 |
| `useBreakpoint` / antd `Grid` | **0 hits** in `src/` | — | none in use |

`grep -rn 'breakpoint|useBreakpoint|collapsedWidth|\bGrid\b' src/` returns exactly two lines:
`AppShell.tsx:159`, `:160`.

### 1.2 Every `Col` with a breakpoint prop — 36 sites, 7 files

| file | lines | prop shape | count |
| --- | --- | --- | --- |
| `pages/StatesGalleryPage.tsx` | 59, 74, 107, 154, 174, 208, 227, 253, 307, 336, 370, 397, 426, 445, 461, 490, 515, 538 | `lg={12} xs={24}` | 18 |
| `pages/OverviewPage.tsx` | 106, 109, 117, 120 | `lg={6} sm={12} xs={24}` | 4 |
| `pages/OverviewPage.tsx` | 143, 208 | `lg={15} xs={24}` / `lg={9} xs={24}` | 2 |
| `pages/GuidelinesPage.tsx` | 178, 321, 375, 385, 437 | `md={12} xs={24}` | 5 |
| `pages/MonitoringPage.tsx` | 183, 188 | `lg={12} xs={24}` | 2 |
| `pages/ComposePage.tsx` | 277, 344 | `lg={15} xs={24}` / `lg={9} xs={24}` | 2 |
| `pages/ProjectsPage.tsx` | 181 | `lg={8} md={12} xs={24}` | 1 |
| `components/ServiceMonitor.tsx` | 246, 251 | `lg={12} xs={24}` | 2 |

Two consequences worth stating before the ticket starts:

- **`lg` is antd's 992, so the mock's "desktop" begins at 992 today** — not 1200. There is no tablet tier
  to find: 992–1199 renders the desktop composition minus the sider.
- **antd's breakpoints already have a 1200 rung**: `xs<576, sm≥576, md≥768, lg≥992, xl≥1200, xxl≥1600`.
  So "desktop ≥ 1200" is `xl`, and no custom breakpoint is needed; tablet 768–1199 is **`md ∪ lg`** and
  mobile < 768 is **`xs ∪ sm`**. A tablet column that is 2-across at both 768 and 1024 needs `md={12}`
  *and* `lg={12}` — the current `lg`-only props cannot express that.


### 1.3 Every `clamp()` and the `LAYOUT` constants

| file:line | value | used by | breaks at |
| --- | --- | --- | --- |
| `app/theme.ts:24` | `siderWidth: 248` | `AppShell.tsx:162`, and the width of the future nav Drawer | 390: a 248 drawer leaves 142px of backdrop — a decision, not a default |
| `app/theme.ts:25` | `barHeight: 56` | header (`theme.ts:191`), sider logo row, FullscreenEditor bar (`FullscreenEditor.tsx:224`) | no |
| `app/theme.ts:26` | `contentMaxWidth: 1280` | `AppShell.tsx:275` | no — at 1200 viewport minus a 248 sider the column is 952, so 1280 never binds below ~1528 |
| `app/theme.ts:27` / `:28` | `gutter: 24` / `gutterSmall: 16` | `AppShell.tsx:274`, `FullscreenEditor.tsx:284` | 390: 342px of content — workable, but the PageHeader action row has no slack |
| `app/theme.ts:37` | `editorMinHeight: 'clamp(640px, 80vh, 1000px)'` | `ComposePage.tsx:279` | **yes** — §6 |
| `app/theme.ts:44` | `volumeEditorHeight: 'clamp(320px, 45vh, 560px)'` | `VolumeExplorerPage.tsx:557` | 390: 320px min inside a stacked card is the floor the ticket wants; 45vh of 844 = 380 — re-check, don't assume |

Only two CSS `clamp()`s exist. `data/mocks.ts:538,550,685,703` is a numeric `clamp` helper for the metric
fixtures, not CSS.

### 1.4 Hard-coded px widths/heights

| file:line | value | note |
| --- | --- | --- |
| `app/AppShell.tsx:199` | `calc(100vh - ${LAYOUT.barHeight}px - 37px)` | **the `37` duplicates `PrototypeStrip`'s `minHeight: 37` (`:301`)**; at 390 the strip wraps to 2–3 lines and this box overflows the viewport |
| `app/AppShell.tsx:301`, `:313-322` | `minHeight: 37` + `Segmented` | strip is one `justify="space-between"` row: long text + Instant/Normal/Slow |
| `app/AppShell.tsx:169-174` | logo chip `28×28` | fine |
| `components/DangerModal.tsx:51` | `width={520}` | antd clamps a Modal to the viewport; it is still not a sheet |
| `components/StartHandleDialog.tsx:69`, `:93`, `:152-171` | `width={640}`, `labelStyle width 160`, `Row gutter={12}` + two `Col span={12}` | uid/gid inputs ≈ 160px each at 390; ~200px for values |
| `components/CommandPalette.tsx:83`, `:95` | `width={560}`, `maxHeight: 420` | clamps; the palette is a **non-goal** in the port (`.scratch/design-port/spec.md:33`) |
| `components/FullscreenEditor.tsx:205`, `:215`, `:285` | body `height: 100vh`, `width="100vw"`, rail `width: 340` | 340 = 87 % of a 390 screen → the rail must stack (§4) |
| `pages/ComposePage.tsx:579` | Drawer `width={760}` | **antd's Drawer does not clamp to the viewport the way Modal does** — at 390 this is a 760px fixed panel. Must become `min(760, 100vw)` |
| `pages/ComposePage.tsx:588`, `:598`, `:624` | `minWidth: 240`, `width: 150`, `maxHeight calc(100vh - 220px)` | the logs drawer's filter row and its body height |
| `pages/ProjectPage.tsx:497`, `:518` | modal `width={840}`, Monaco `height={320}` | `components/VariablesPanel.tsx:261` is the same shape at `width={720}` |
| `components/VariablesPanel.tsx:302` | `scroll={{ y: 220 }}` | the repo's **only** `scroll` prop — vertical only, no `x` (§3) |
| `pages/VolumeExplorerPage.tsx:822`, `pages/StatesGalleryPage.tsx:502` | `labelStyle={{ width: 120 }}` | |
| `pages/ProjectsPage.tsx:134, :145, :192, :195` | `maxWidth: 320`, `width: 190`, `minHeight: 148`, `maxWidth: 150` | filter row + card title |
| `pages/MonitoringPage.tsx:172` | `maxWidth: 760` | description text |
| `components/ServiceMonitor.tsx:193` | `maxWidth: 420, minWidth: 280` | the service `Select` — **280px of min-width inside a 390 viewport** |
| `components/MetricChart.tsx:23-24` | `PLOT_HEIGHT = 140`, `MARGIN.left = 46`, `MARGIN.right = 8` | fixed-pixel plot geometry (§6/§8) |
| `pages/AuthPages.tsx:21` | left panel `flex: '0 0 44%'` | 172px at 390 — a split-screen assumption (§8) |
| `pages/AuthPages.tsx:44`, `:74` | `maxWidth: 420` / `440` | fine |
| `src/index.css:14` | `body { min-width: 320px }` | the narrowest width the mock allows; the 360 check sits just above it |
| table column `width:` totals | §3 | explicit widths that sum far past 390 |

---

## 2. Hard-coded pixel assumptions that break when the desktop floor moves 992 → 1200

**What 1024 renders today:** `1024 ≥ 992`, so the sider is **visible** at full 248px, and every `lg` prop is
active. 1024 is therefore not "a phone" — it is the desktop composition in a 1024 window. That is the state
the floor move changes, and it is why the two halves of the move (shell breakpoint, `Col` props) must land
together: moving only the shell to 1200 gives 1024 a **drawer plus a desktop grid**, the worst of both.

| hit | file:line | does 1200 change what renders at 1024? |
| --- | --- | --- |
| Sider `breakpoint="lg"` | `app/AppShell.tsx:159` | **YES — the biggest change in the ticket.** 1024 today: 248px sider, 776px of content. Under 1200: sider gone, must be a Drawer behind a menu button, content 1024px wide |
| `collapsedWidth={0}` | `app/AppShell.tsx:160` | YES, and it is the bug: below the floor the navigation is deleted, not moved. Applies at **both** 992 and 1200 — the drawer is mandatory under either option |
| `Col lg={6} sm={12}` ×4 (stat rows) | `pages/OverviewPage.tsx:106,109,117,120` | YES: 4-across today → `sm` picks 2-across at 1024 once the floor is 1200. Reads as a tablet stat grid, which is the intended tablet design — but it must be *designed* (2×2 with the icon chip), not inherited |
| `Col lg={15}` / `lg={9}` (Overview) | `pages/OverviewPage.tsx:143,208` | YES: the 15/9 split collapses to two stacked full-width cards (Composes list, then Activity). A content reordering, not a reflow |
| `Col lg={15}` / `lg={9}` (Compose) | `pages/ComposePage.tsx:277,344` | **YES, and it is a real design decision** — §8 flags Compose as a redesign screen: the editor's "as tall as the rail beside it" rule (`GUIDELINES.md:331`) stops being true the moment the rail is under the editor |
| `Col lg={12}` ×2 (charts) | `pages/MonitoringPage.tsx:183,188`, `components/ServiceMonitor.tsx:246,251` | YES: 2-across → 1-across at 1024, six charts stacked |
| `Col lg={8} md={12}` (card grid) | `pages/ProjectsPage.tsx:181` | YES: 3-across → `md`'s 2-across at 1024 |
| `Col lg={12}` ×18 (states) | `pages/StatesGalleryPage.tsx` (18 lines, §1.2) | YES: 2-across → 1-across. The design-system pages are the least important half of this audit |
| `Col md={12}` ×5 | `pages/GuidelinesPage.tsx:178,321,375,385,437` | no — `md` is 768, unchanged by the floor move |
| `Descriptions column={{lg:3,sm:2,xs:1}}` | `components/ServiceMonitor.tsx:221-222` | YES: the facts strip drops from 3-across to `sm`'s 2-across at 1024. This is the **only** responsive prop in the mock that is not a `Col`, so a `lg`→`xl` sweep misses it |
| `contentMaxWidth: 1280` | `app/theme.ts:26` | no at 1024; noted because the column never binds below 1528 with a 248 sider |
| `editorMinHeight` clamp | `app/theme.ts:37` | no at 1024 (80vh of a typical 768–900 window clamps to the 640 floor either way). The 390 case is §6 |
| `volumeEditorHeight` clamp | `app/theme.ts:44` | no |
| the strip's `37` | `app/AppShell.tsx:199,301` | no at 1024; it is a 390 bug |
| fixed modal widths 520 / 640 / 720 / 840 | §1.4 | no at 1024 — antd clamps all of them and none exceeds 1024 |
| Drawer `width={760}` | `pages/ComposePage.tsx:579` | at 1024 the Drawer is 760 of 1024 (74 %) — tolerable; at 390 it is the bug in §1.4 |

## 3. Every `<Table>` site — 14 tables in 8 files

**The structural fact first:** `grep -rn 'scroll=' src/` finds exactly **one** hit —
`components/VariablesPanel.tsx:302 scroll={{ y: 220 }}` — and it is vertical. There is **no `scroll={{ x }}`
anywhere in the mock.** So today every table below its natural width relies on `table-layout: auto` and
cell wrapping, which is precisely why ticket 03's acceptance test (`documentElement.scrollWidth <=
innerWidth`, per screen per width) will fail on most table screens rather than merely look cramped.

| # | file:line | screen / what | columns (`width:`) | the decision columns | droppable at phone width |
| --- | --- | --- | --- | --- | --- |
| 1 | `pages/OverviewPage.tsx:234` | All Composes (`size="middle"`) | Compose (flex), Project / Environment (240), Containers (200), Last edited (160) | name + `ComposeStatusTag`, Containers (`ComposeStatusInline`) | Last edited; Project / Environment folds into a secondary line under the name |
| 2 | `pages/ProjectsPage.tsx:227` | table view | Project (flex, sortable), Description (flex, `maxWidth: 360`), Envs (90), Composes (110), Containers (180), Updated (160), actions (70) | Project, Containers, actions (⋮) | Description, Envs, Updated, and the two sortable columns |
| 3 | `pages/ProjectPage.tsx:242` | Composes tab | Compose (flex), Containers (200), **Deploy identity (240)**, Last edited (150), actions (220: Open + Deploy/Stop + ⋮) | Compose, Containers, one action (Deploy) | Deploy identity (a 30-char slug), Last edited, and two of the three actions → ⋮ |
| 4 | `pages/ProjectPage.tsx:371` | Settings → Environments (`size="small"`, `:418`) | Environment (flex), `n Composes` (160), Created (200), delete (70) | Environment, delete | Created; the count folds into the name cell |
| 5 | `components/VariablesPanel.tsx:149` | Variables (`size="middle"`) | Key (260), Value (flex, `ValueCell`), secret/plain (90), Updated (140), actions (90: edit + `Popconfirm` delete) | Key, Value (masked + reveal), secret tag | Updated; the 90px type tag merges into the Key cell |
| 6 | `components/VariablesPanel.tsx:285` | paste-`.env` preview, **inside the 720px Modal** | Key (240), Value (flex) | Key, Value | — (2 columns already) |
| 7 | `pages/VolumeExplorerPage.tsx:508` | file list | Name (flex, symlink target inline), Size (110), Owner (110), Mode (90), Modified (150), actions (130: edit + ⋮ + `Popconfirm`) | Name, Size, Modified, and one destructive action | **Owner, Mode** — exactly as the ticket says; the uid:gid/mode detail belongs behind the ⋮ |
| 8 | `components/ComposeVolumesTab.tsx:242` | Compose → Volumes, **`tableLayout="fixed"` (`:248`)** | Volume (190), Source on the host (340), In the container (flex), Mounted by (110), Handle (210), actions (190) | Volume (+ kind/ro tags), Handle state, one action | Source on the host → a copyable second line; Mounted by (tags) |
| 9–11 | `pages/StatesGalleryPage.tsx:90, :161, :569` | design-system page | 2 / 2 / 3 columns, widths 160 / 200 / 220 | n/a — a documentation table | nothing; see the scope note below |
| 12–14 | `pages/GuidelinesPage.tsx:77, :219, :485` | design-system page | 4 / 4 / 3 columns, widths 160 / 160 / 240 | n/a — a documentation table | nothing; see the scope note below |

**Worst offenders, ranked:**

1. `ComposeVolumesTab.tsx:242` — **1040px of explicit column widths plus a flexible column, under
   `tableLayout="fixed"`**. `fixed` means the browser will not shrink the columns, so at 390 this table is
   ~2.7 viewports wide and will push the document. This is the single clearest "stacked rows or nothing" case.
2. `VolumeExplorerPage.tsx:508` — 6 columns, 460px of fixed widths plus a Name cell that renders a symlink
   target and a warning `Tag` inline (`:669-682`), so the Name column alone can exceed 390.
3. `ProjectPage.tsx:242` — 810px of fixed widths in a 390 space, with a 220px action cell holding three
   controls.
4. `OverviewPage.tsx:234`, `ProjectsPage.tsx:227` — 600px and 610px of fixed widths respectively.

**Scope question the ticket has to answer** (also in `Uncertain`): `StatesGalleryPage` and `GuidelinesPage`
are design-system/feedback screens, they are 5 of the 14 tables, and 04 keeps `#/design/states` reachable as
a *capture host* for the ~10 feedback scenes with no home screen. Their table framing at 390 is only worth
designing if those pages get a phone frame; the ticket's own checklist ("Every `<Table>` screen shows
stacked rows at phone width") reads as if they do.

**Also on the stacked-row mechanics:** rows that are clickable today
(`OverviewPage.tsx:280-283`, `ProjectsPage.tsx:299-302`, `ProjectPage.tsx:322-325`,
`VolumeExplorerPage.tsx:514-527` — the last one navigating on a directory row) carry the whole affordance in

## 4. Every overlay site

**Totals:** 11 `<Modal>` sites across 9 files, 6 `<DangerModal>` call sites, 2 `<StartHandleDialog>` call
sites, 1 `<Drawer>`, 3 `<Popconfirm>`s. Every one is currently an antd default or a fixed px width — **not
one is a sheet.**

| overlay | definition | call sites | today | 834 | 390 |
| --- | --- | --- | --- | --- | --- |
| `DangerModal` | `components/DangerModal.tsx:42-73`; `width={520}` (`:51`) | `ProjectsPage.tsx:336` (delete Project), `ProjectPage.tsx:528` (delete Compose), `:552` (delete Project), `ComposePage.tsx:641` (delete Compose), `StatesGalleryPage.tsx:609, :628` | centred antd modal | centred, unchanged | **full-height sheet**; the typed-name `Input` (`:59-64`) and the disabled-until-match OK button (`:45`) are one control pair and must both be above the fold; the banner `Alert` at `:66-72` uses `margin: '16px -24px -20px'`, i.e. it assumes the modal's 24px padding — a sheet with different padding clips it |
| New / Edit Project | `pages/ProjectsPage.tsx:309-334` | — | antd default width (520) | centred | sheet |
| New Environment | `pages/ProjectPage.tsx:455` | — | antd default | centred | sheet |
| New Compose | `pages/ProjectPage.tsx:487-526`; `width={840}` (`:497`), Monaco `height={320}` (`:518`) | — | 840 + a fixed-height editor + an `Alert` (`:520`) | centred (834 → clamped to ~802) | sheet, **and the editor inside it is a second Monaco that must survive the resize** (§6) |
| New / Edit variable | `components/VariablesPanel.tsx:219` | — | antd default | centred | sheet |
| Paste a `.env` | `components/VariablesPanel.tsx:247-308`; `width={720}` (`:261`), preview `<Table scroll={{ y: 220 }}>` (`:285-305`) | — | 720 + an 8-row `TextArea` + a 220px-tall preview table | centred | sheet; the OK button's label is the whole consequence statement (`:251`, "Replace N variables with M") and must stay visible |
| New file / folder | `pages/VolumeExplorerPage.tsx:575-596` | — | antd default | centred | sheet |
| Download archive | `pages/VolumeExplorerPage.tsx:788-845` | — | antd default | centred | sheet; the symlink warning (`:826-841`) is the one fact that decides whether extracting is safe — it cannot be the thing that falls below the fold |
| `StartHandleDialog` | `components/StartHandleDialog.tsx:59-202`; `width={640}` (`:69`) | `pages/VolumeExplorerPage.tsx:313`, `components/ComposeVolumesTab.tsx:256` | 640, `Descriptions` at `labelStyle 160` (`:93`), uid/gid `Row` (`:152-171`), two warnings (`:115-129`, `:186-201`) | centred | sheet, and this is the destructive-control case the ticket names: "Open Volume" (`:64`) is disabled when the path is absent (`:63`) and the write-while-running warning (`:186-201`) names the services — both must be reachable at 390 |

## 5. `Segmented` / `Tabs` / tab bars that must scroll, never wrap

antd's `Tabs` has built-in overflow (a `more` dropdown); **antd's `Segmented` has none** — it shrinks, then
overflows its container. So the `Segmented` sites are the real work and the `Tabs` sites need a check, not a
fix.

| site | file:line | why it wraps today | at 390 |
| --- | --- | --- | --- |
| Mock-latency strip | `app/AppShell.tsx:313-322` (`size="small"`) | the strip is one `justify="space-between"` row with a long sentence beside it (`:305-310`) | must stop taking a full row of the viewport (ticket body): one icon + the control, or behind the overflow menu |
| Projects: Cards / Table | `pages/ProjectsPage.tsx:148` | sits in `Flex wrap` (`:126`) beside a filter `Input` (`maxWidth: 320`) and a sort `Select` (`width: 190`) | the row stacks; the `Segmented` must scroll rather than drop to a second line |
| Project: Environment switcher | `pages/ProjectPage.tsx:192-207` | one option per Environment, each with a count `Tag` — unbounded | **the most likely to overflow**: N environments × ~110px, so horizontal scroll with the active one scrolled into view |
| Project: Composes / Variables / Settings | `pages/ProjectPage.tsx:214` | antd `Tabs` | fits (3 short labels) |

## 6. Monaco

`MONACO_OPTIONS` — `components/YamlEditor.tsx:7-17`:

```
minimap {enabled:false} · fontSize 13 · fontFamily CODE_FONT_STACK · lineHeight 20
scrollBeyondLastLine false · renderLineHighlight 'gutter' · tabSize 2 · smoothScrolling true
padding {top:16,bottom:16}
```

Applied at `components/YamlEditor.tsx:57` (`{...MONACO_OPTIONS, readOnly}`) and
`components/TextFileEditor.tsx:67` (same spread).

### 6.1 The premise needs re-testing, not a one-line fix

The spec (§*Viewport and theme*) and 03's checklist both say `MONACO_OPTIONS` lacks `automaticLayout`, so a
container whose width changes will not re-layout its editor. **The evidence in this repo says that is already
false:**

| evidence | where |
| --- | --- |
| `@monaco-editor/react@4.7.0` creates the editor as `editor.create(el, { model, **automaticLayout: true**, ...options }, services)` — `automaticLayout` is set *before* the options spread, so it applies **unless** the options object overrides it | `node_modules/@monaco-editor/react/dist/index.mjs`, the `Editor` component's `create` call |
| `MONACO_OPTIONS` has no `automaticLayout` key → the effective value is `true` | `components/YamlEditor.tsx:7-17` |
| Monaco 0.55's auto-layout uses a real `ResizeObserver` on the editor's container, so a container width change re-lays out on the next animation frame — no window resize, no reload | `node_modules/monaco-editor/esm/vs/editor/browser/config/elementSizeObserver.js` (`_resizeObserver.observe(this._referenceDomElement)`) and `editorConfiguration.js:53` reading `EditorOption.automaticLayout` |

So **03's Monaco item should be an assertion, not an edit.** Adding `automaticLayout: true` to
`MONACO_OPTIONS` is a no-op that documents intent (and it guards against someone later writing
`automaticLayout: false`); it is not the fix the ticket describes. If the CDP check *does* fail, the cause is
more likely one of these, and the ticket should look here first:

- **an editor created while its container is hidden or zero-width** — `pages/ProjectPage.tsx:487-526`'s
  Monaco sits inside a `Modal` with `destroyOnHidden` (`:492`) and is created at `height={320}` (`:518`)
  while the modal animates in; Monaco clamps a measured size to `≥5px`, so the first layout can be 5px wide
  until the observer corrects it;
- **a container with no width chain to shrink into** — the Compose editor is `div {flex:1, minHeight:0}`
  (`pages/ComposePage.tsx:334`) inside a Card body `flex:1, minHeight:0` (`:280`), which is the pattern that
  works; `components/FullscreenEditor.tsx:266` uses `flex:1, minWidth:0`, also fine. Any new wrapper the
  ticket adds needs `minWidth: 0`, or the editor will refuse to shrink.

### 6.2 Every place a container's width changes under an editor

| # | site | what changes | at 834 / 390 |
| --- | --- | --- | --- |
| 1 | `pages/ComposePage.tsx:277` (`Col lg={15}` → `xs={24}`), card `minHeight: LAYOUT.editorMinHeight` (`:279`), body `flex/minHeight:0` (`:280`), editor wrapper (`:334`) | the editor goes from a ~⅔ column to full width when the Row stacks | the primary case; the `clamp()` height must become viewport-based |
| 2 | `components/FullscreenEditor.tsx:265-276` — editor `flex:1, minWidth:0` beside the rail `width: 340` (`:285`) | the rail stacks under the file at 390 → editor width goes `100vw − 340` → `100vw` | the second Monaco; same change, opposite direction |
| 3 | `pages/VolumeExplorerPage.tsx:566` inside a Card with body height `LAYOUT.volumeEditorHeight` (`:557`) | the toolbar above it (`:471-504`, five buttons in a `Flex wrap`) wraps to more rows | width stable; height changes if the clamp changes |
| 4 | `pages/ProjectPage.tsx:518` (`height={320}`, inside the 840px Modal) | the Modal becomes a sheet; the editor is created while the modal mounts | §6.1's first bullet |
| 5 | Board Live frames (spec, *Viewport and theme*) | the board resizes the iframe → the iframe's own window resizes → the observer fires | the same mechanism as a phone-width reflow, which is why the spec ties the two together |
| 6 | `pages/ComposePage.tsx:335` + `components/FullscreenEditor.tsx:267` | two editors on **one** Monaco model, hence `keepCurrentModel` (`YamlEditor.tsx:52`, `FullscreenEditor.tsx:268`) | not a width issue, but a reflow must not dispose the shared model — one assertion while the ticket is in there |

| Compose: Editor / Monitor / Volumes | `pages/ComposePage.tsx:269-520` | antd `Tabs`, one label carries a `Badge` (`:512`) | fits; the badge must not be clipped |
| Monitoring: window | `pages/MonitoringPage.tsx:160-164` | it is in `PageHeader`'s `extra`, which is `Flex wrap` (`components/primitives.tsx:147`) | **wraps under the title rather than scrolling** — either accept that as the design and say so, or use the scroll container |
| Compose → Monitor: window | `components/ServiceMonitor.tsx:197-201` | `Flex wrap` (`:173`) beside a `Select` with `minWidth: 280` (`:193`) and a Refresh button | two rows; the `Select`'s 280px min-width is the binding constraint |
| Gallery controls | `pages/ScreensGalleryPage.tsx:280, :294, :305` | the page is deleted by 09 (`.scratch/design-board/spec.md:599-602`) | **not 03's** |

| `FullscreenEditor` | `components/FullscreenEditor.tsx:197-334`; `width="100vw"` (`:215`), body `height: 100vh` (`:205`), rail `width: 340` (`:285`) | `pages/ComposePage.tsx:522-534` | already full-screen (100vw × 100vh), editor beside a 340px rail | **already a sheet — 834 is where the rail must stop being beside the file** | sheet with the rail **under** the editor; the header cluster (`:224-261`: Modified, Discard, Save, Close) must not wrap; Monaco is the only scrollable region |
| Logs | `pages/ComposePage.tsx:536-639`; `width={760}` (`:579`) | — | **`Drawer`, fixed 760px** | 760 of 834 — nearly full width already | **760 of 390.** antd's Drawer does not clamp to the viewport the way Modal does, so this overflows. `min(760, 100vw)` or a sheet; then the filter row (`:581-610`) stacks and the `pre`'s `calc(100vh - 220px)` (`:624`) is wrong |
| Command palette | `components/CommandPalette.tsx:76-127`; `width={560}` (`:83`) | `app/AppShell.tsx:279` | antd default chrome | fine | clamps; but the palette is a **non-goal** in the port (`.scratch/design-port/spec.md:33`), so a phone design for it is optional |
| `Popconfirm` ×3 | `components/VariablesPanel.tsx:195` (delete one variable), `pages/ProjectPage.tsx:393` (delete an Environment), `pages/VolumeExplorerPage.tsx:737` (delete a file/dir) | — | anchored popover beside the trigger | still works — a tablet has room beside a card | **dialog whose body names the subject row** (no hover, and the arrow points at a 24px icon). Each already names its subject in `description`: `VariablesPanel.tsx:198`, `ProjectPage.tsx:394` (`This stops and removes every Compose in it (N)`), `VolumeExplorerPage.tsx:739-745` ("and everything under it … no undo") — the dialog inherits that copy, it does not invent new it |

`onRow`. A stacked row keeps the click target but loses the header that named the column, so every stacked
cell needs its label inline — that is the design work, not the CSS.

---

| table column sums (§3) | §3 | no at 1024 for most tables; **`ComposeVolumesTab` sums ~1040–1250px of declared columns under `tableLayout="fixed"` (`:248`)**, which is already wider than a 1024 card |
| `0` uses of `scroll={{ x }}` | §3 | no at 1200; it is what makes the 390 sweep fail |

**Also note:** `GUIDELINES.md:343-344` is the four-sentence contract this replaces ("below 992px the sider
collapses to zero, page actions wrap under the title, tables scroll horizontally, and the Compose rail
stacks under the editor") — and `pages/StatesGalleryPage.tsx:336-348` is the same text plus a static 360px
card (`maxWidth: 360` at `:348`). Both are 03's to rewrite; §4.9 is 09's.

---


## 7. Hit targets

| class | count / where | height today | verdict at 390 |
| --- | --- | --- | --- |
| **every default control** | antd's default control height is **32px**, and `GUIDELINES.md:342` makes it a rule: "Controls stay 32px tall (DESIGN.md); only in-table actions drop to `size="small"`" | 32 | **fails 44px — as a rule, not an oversight.** §6 must be amended with a phone-width rule, or the acceptance criterion scoped; the two documents cannot both stand |
| in-table / in-row actions | ~45 sites with `size="small"`, listed below | antd small = **24px** | all fail; §3 is where they become rows, so the fix belongs there rather than as a global size bump |
| theme `Switch` | `app/AppShell.tsx:248-254` (`size="small"`) | ~16px | the worst offender, in the header cluster the ticket is already redesigning |
| `Badge` | `app/AppShell.tsx:241`, `pages/ComposePage.tsx:512` | 14–16px | decorative; the Compose badge sits on a tab, so the target is the tab |
| `Input` / `Select` in dense rows | `pages/ComposePage.tsx:588, :598`; `components/ServiceMonitor.tsx:176-195` | 32 | fail; the logs drawer's filters move into the sheet's own row |
| `Tooltip`-wrapped icon buttons | `pages/ComposePage.tsx:348-356`; `pages/VolumeExplorerPage.tsx:284-286, :500-502` | 32 | fail — and a tooltip is not an affordance on a phone, which is why the ⋮/overflow pattern is the answer |
| `Descriptions size="small"`, `Card size="small"` | `components/StartHandleDialog.tsx:94`, `pages/ComposePage.tsx:470`, `pages/StatesGalleryPage.tsx:65,68,113,124` | not interactive | n/a — listed so they are not mistaken for targets |

`size="small"` sites by file (each 24px today):
`app/AppShell.tsx:241, :251, :320`; `pages/ComposePage.tsx:298, :308, :321, :324, :353, :380, :444, :470, :512, :545, :550, :563, :603`;
`pages/OverviewPage.tsx:89, :177, :203`; `pages/ProjectPage.tsx:288, :296, :305, :311, :408, :418, :513`;
`pages/StatesGalleryPage.tsx:87, :101, :116`; `pages/GuidelinesPage.tsx:150, :263, :554`;
`pages/ScreensGalleryPage.tsx:286, :297, :308, :316, :321, :350` (page deleted by 09);
`components/ComposeVolumesTab.tsx:195, :202, :212`; `components/FullscreenEditor.tsx:90, :104, :125`;
`components/LiveHandlesBanner.tsx:68, :96`; `components/ServiceMonitor.tsx:235`; `components/StartHandleDialog.tsx:94`;
`components/VariablesPanel.tsx:43, :194, :203, :303`; `pages/VolumeExplorerPage.tsx:718, :735, :750`.

---



## 8. Per screen, at 834 and at 390

Chrome above the content costs ~180–220px at both widths (56px header + strip + 24px padding + PageHeader),
so the first viewport holds ~620px of content at 390 and ~890px at 834 (1112 is *taller* than the desktop
1000, which is why 834 is a real tier rather than a squeeze).

| screen | 834 | 390 | first viewport at 390 | flag |
| --- | --- | --- | --- | --- |
| **Overview** | stat row 4-across → 2×2 (`OverviewPage.tsx:106,109,117,120`); the 15/9 row (`:143,208`) stacks, so the composes list with the live-handle banner comes before Recent activity; "All Composes" (`:234`) still fits at 760px | stat cards 1-across today (`xs={24}`) = ~420px, i.e. the whole viewport → **must become 2-across or lose two to the fold**; the 4-column table → stacked rows keeping Compose + status, dropping Project/Environment and Last edited; the 6-item Timeline drops or moves below the table | title + the attention alert (`:84-100`) + at least one stat | **YES** |
| **Projects** | card grid 3→2-across (`:181`, `md={12}`); filter row (Input 320 + Select 190 + Segmented) still one line at 786px | filter row stacks; the Cards/Table `Segmented` (`:148`) must scroll; cards 1-across (`minHeight: 148` is fine); table → stacked rows keeping Project + Containers + ⋮, dropping Description, Envs, Updated | title + New Project + the first card | no |
| **Project** | Environment `Segmented` (`:192`) starts to overflow → scroll; Composes table (810px of fixed widths) fits at 786 but tight; the "New Compose" row stacks; Settings: form, Environments table (430px fixed) and danger zone all fit | Environment switcher scrolls with the active chip in view; **Composes table → stacked cards** keeping name + Containers and one primary action (Deploy/Stop) with the rest in a ⋮, dropping Deploy identity (a 30-char slug) and Last edited; Settings: Environments table → stacked, Created dropped; danger zone stacks | title + Environment switcher + tab row + New Compose | **YES** (Composes tab; Variables/Settings are reflows) |
| **Compose** | Editor tab stacks (`:277,344`) → editor full width, then Status / Deploy identity / Environment / Activity; at 834×1112 the `clamp` resolves to 890px inside the clamp, so the page is ~1800px tall; the header's four actions (`:235-255`) wrap under the title and must keep "one primary, one More" (`GUIDELINES.md:325-327`) | editor becomes viewport-based; header extra collapses (Deploy + ⋮ + Logs, Save folding into the dirty banner); tabs fit; Monitor: `Segmented` + `Select minWidth 280` + Refresh take 2–3 rows, charts 1-across; Volumes: `ComposeVolumesTab` stacked rows; Logs Drawer → sheet | title + status tag + Deploy | **YES — the biggest** |
| **Volume Explorer** | PageHeader extra (mode tag, uid:gid, closes-in, Close, open-in-new-tab) wraps to 2 rows; the 5-button toolbar (~500px) fits; 6-column file list: Name is squeezed because the symlink target renders inline (`:669-682`) → Owner or Mode must already go here | breadcrumb truncates **in the middle**; toolbar → one overflow button; file list → stacked rows keeping Name / Size / Modified, dropping Owner and Mode; the symlink target and the "outside the Volume" tag stay (they are why the screen exists); row edit/⋮/delete → one ⋮ + a sheet; the file editor card (`volumeEditorHeight`, 320px min) sits below a long list | title + Read-write tag + the toolbar's overflow + the first two rows | **YES** |
| **Monitoring** | chart pairs go **2-across** — a tablet *addition*, since `lg={12}` is false at 834 today; the window `Segmented` (`:160`) sits in PageHeader `extra`, which wraps | 6 charts 1-across; plot ≈ 310px, inner 256px; `maxTicks = floor(256/64) = 4` (`MetricChart.tsx:109`), so 24h picks 360-min steps → 4 labels; downsampling gets 128 buckets (`:81-103`); the 10px labels and the 46px left margin stay legible. **The rule holds — this is a check** | title + window control + the first chart | no |
| **Variables** | the 5-column table (580px fixed + a flexible Value) fits at 786; search `maxWidth: 280` + New + paste `.env` fit | stacked rows: Key (mono) + masked value + reveal on one line, the row itself as the tap target (the reveal button is 24px today); secret/plain inline; Updated dropped; edit/delete → ⋮ + a sheet; the paste-`.env` Modal (720) → sheet with its 2-column preview | title + the count line + the first two variables | no |
| **Settings** | form (`maxWidth: 360/560`) unchanged; Environments table (430px fixed) fits; the danger-zone row stacks | form full width; Environments table → stacked (name + `n Composes` + delete icon), Created dropped; danger zone stacks; Delete Project is the sheet `DangerModal` | title + the form's first field | no |
| **Auth** | the `flex: '0 0 44%'` panel (`AuthPages.tsx:21`) is 367px of 834 with 40px padding — the level-2 headline and the 3 feature lines are cramped; the card (`maxWidth: 440`) fits | the panel is **172px wide with 40px padding = 92px of content. It cannot stay as-is** — drop it, or re-cast it as a top band; Setup's `Steps` (`:148-152`) and the 3 tag chips (`:216-218`) must not wrap | the form's first field | **YES** |
| **States** | 2-across under the new tier (today 1-across at 834); 3 tables | 1-across; the "Narrow viewports" card (`:336-348`) and its static 360px illustration become a link to the tablet/mobile scenes — **03's deliverable**, and the only place the mock documents its own responsive behaviour today | title + the first card | no (but the card rewrite is 03's) |
| **Guidelines** | 5 cards 2-across (`md={12}`, unchanged); 3 tables (4/4/3 columns) | 1-across; tables → stacked | title + §1–§5 nav | no |

### 8.1 Screens where 390 forces a genuine redesign — the reviewer's short list

The ticket's last checklist item asks for exactly this. **Seven, in the order a reviewer should look:**

1. **Compose** — the editor/rail height contract dies the moment the columns stack (`GUIDELINES.md:331`),
   and the header's four actions (`ComposePage.tsx:235-255`) have to become one primary + a ⋮. None of this
   is a reflow.
2. **Volume Explorer** — six filesystem columns to three, a five-button toolbar to one button, a breadcrumb
   that truncates in the middle, and a directory delete that moves out of a `Popconfirm` into a sheet.
3. **Project → Composes tab** — the widest row in the product (810px of fixed columns, a 220px cell with
   three controls, a 30-character deploy identity) becomes a card with one primary action.
4. **Auth** — a 44%-width panel that the file itself says exists so the screen does not read as an error
   page (`AuthPages.tsx:8-12`) has no 390 form unless it is dropped or recast.
5. **Overview's first viewport** — a 4-across stat row that becomes four stacked cards, i.e. the screen's
   entire first viewport. The decision is what "is anything broken?" costs at 390.
6. **`FullscreenEditor`** — a 340px rail beside a 100vw editor has no 390 answer except "under", and then the
   editor *is* the screen; the Modified/Discard/Save/Close cluster (`:224-261`) must survive that.
7. **`DangerModal` as a sheet** — type-the-name plus consequences plus "cannot be undone" in the first
   viewport, with the enable-OK feedback still visible. It is a **pattern, not a screen**, which is why it is
   listed separately: five call sites inherit whatever is decided.

**Reflows, not redesigns** (the reviewer can look at these last): Projects, Variables, Settings, Monitoring,
States, Guidelines, 404.

### 8.2 Where the tablet tier *adds* rather than removes

Worth saying out loud, because "tablet = phone but wider" is one side of open question 2: at 834 today `lg`
is false, so Overview's stats are 1-across, Monitoring's charts are 1-across, the States cards are 1-across
and Projects' cards are `md`'s 2-across. A designed tablet tier therefore *adds* the 2×2 stat row, the chart
pairs and a 2-across documentation grid. That is the concrete difference between "a tablet tier" and "a wide
phone", and it is the strongest single argument in these notes for question 2.

---

## 9. What ticket 03 must not do

**Non-goals that touch it** (`spec.md:761-777`):

- **No touch gestures, no PWA.** "Responsive design here means the screens work at 834 and 390 — not swipe
  gestures, pull-to-refresh, or installing the panel on a home screen" (`spec.md:769-771`): no swipe to open
  the nav drawer, no pull-to-refresh on a table, no manifest, no service worker.
- **No responsive board.** The canvas reviews three widths and does not run at one (`spec.md:761-763`), and
  neither does the offline `dist-single` copy. 03's phone design must not be extended to the review surface.
- **No second viewport for every state.** ~20–25 mobile and ~10 tablet scenes, the ones whose layout genuinely
  changes (`spec.md:764-767`). 03 designs the tiers; 04 declares `viewports` per scene; 05 captures. 03 must
  not enumerate a frame per state per width, and does not pick the matrix (open question 1).
- **No dark variant.** The registry must not grow a `dark` field (`04:72-73`), and no dark stills are taken.

**What 03 inherits, and must therefore not restate or rebuild:**

| inherited | file:line | 03's obligation |
| --- | --- | --- |
| `data-scene-ready` carries the `?scene=` id and is **withdrawn while the store is pending** | `app/sceneReady.ts:15-49` (`ready` = scene && fontsReady && `Object.keys(pending).length === 0`) | keep both semantics. A width-driven re-render must not leave a stale attribute, and 05's readiness check must still be able to say "this is the scene I asked for" |
| `useUiState` + `?ui=`, all keys declared in one file | `app/uiState.ts:84-93`; `app/uiKeys.ts:23` (`SCREEN_UI_KEYS` is still `[]` — 02 fills it) | the drawer is the only *navigational* state: declare its key in `app/uiKeys.ts`, do not invent a second mechanism |
| the key names 02 reserved for 03 | `02-addressable-states.md:383-403` | `shell.navOpen` (new), `shell.overflowOpen` (new, collapsed header cluster), **reuse** 02's `shell.accountMenu` rather than adding one, and `shell.navGroup` only if the drawer's groups are collapsible |
| the mechanical check that a scene's `ui` keys exist | `04:57`; `app/uiKeys.ts:38-63` (`undeclaredUiKeys`) | a key 03 reads but does not declare warns in dev and forces nothing — 04's assertion is the gate |
| the content-height frame rule (§4.9) and the README prose | `spec.md:612-613` (ticket 09's) | **§6 is 03's; §4.9 and `README.md` are 09's.** Do not rewrite §4.9's frame rule while replacing §6's sentences |
| the contract's two written copies | `GUIDELINES.md:343-344` **and** `pages/StatesGalleryPage.tsx:336-348` | both move together, or the file and the in-app page contradict each other |
| `MONACO_OPTIONS` | `components/YamlEditor.tsx:7-17` | add `automaticLayout: true` only as documentation, after the check in §6.1 — or record in Comments that it was already effective |

**One internal contradiction to fix while in there.** 03's body says "moving the desktop floor from 992 to
1200 is part of the design" (`03-responsive-mobile.md:8`), while its own acceptance line says "Nothing
regresses at ≥ 992px: the desktop review is the same mock it was before this ticket"
(`03-responsive-mobile.md:92`). At 1024 the review *does* change — sider → drawer, 4-across stats → 2-across.
That line becomes ≥ 1200, or the ticket fails its own test.

---

## 10. Uncertain — stated rather than guessed

- **Whether Monaco really re-lays out at runtime.** §6.1 is read from `node_modules` source (strong evidence,
  not a runtime observation). Assert it in CDP — resize a container, read `.monaco-editor`'s width — before
  writing the item off.
- **Whether the document-scroll sweep would catch the Drawer and the sheets.**
  `documentElement.scrollWidth` does not see a `position: fixed` overlay wider than the viewport, so the Logs
  Drawer's 760px (and any sheet) needs `offsetWidth ≤ innerWidth` asserted separately, or the sweep passes on
  a visibly broken screen. The ticket's wording covers the document only.
- **How many Environments the fixtures give a Project** — the Environment `Segmented`'s overflow at 390
  depends on it, and `data/mocks.ts` was not fully enumerated here.
- **Whether `md ∪ lg` is the right translation of "tablet 768–1199".** antd has no single prop for that band,
  so every tablet column needs two props and the shell needs a JS test (`Grid.useBreakpoint()` or
  `matchMedia('(min-width: 1200px)')`) — the mock's first. A custom breakpoint via `ConfigProvider` tokens is
  the alternative and is **untested** in this repo.
- **Whether the two design-system pages are in scope.** States and Guidelines are 5 of the 14 tables and 6 of
  the 36 `Col`s, and 04 keeps `#/design/states` as a capture host. The table checklist reads as if they are;
  the screen list in the ticket does not mention them.
- **Whether the Projects Cards/Table switch survives on a phone.** It is a `Segmented` (`ProjectsPage.tsx:148`)
  whose two options change the presentation of the same data. Dropping it at 390 (defaulting to cards) is a
  decision the ticket has not made.
- **Tablet frame byte cost.** 834×1112 is *taller* than the desktop 1000, so a tablet frame is ~11 % more
  pixels than a desktop one of the same screen while being 58 % of the width. The estimates in
  `open-questions.md` use area (width × height) and need 05's measured bytes before they carry weight.
- **The prototype strip's real height at 390** — `minHeight: 37` assumes one line; the true value comes from
  rendering it, and the sider's `calc(100vh - 56px - 37px)` depends on it.

