# Relist Panel — UX/UI redesign guidelines

Status: **proposal**, reviewed as a clickable prototype in [`web-design/`](.) (`pnpm dev`).
The prototype is mock-only and deliberately not wired to the API.

Scope of this pass: information architecture, screen layouts, state coverage, copy, and the
interaction rules that hold them together. Out of scope: backend changes, routing-library choice,
dark mode as a shipped feature (it is wired here only to prove the tokens hold).

- Domain model is unchanged: Project → Environment → Compose (+ Environment Variables), exactly as
  `CONTEXT.md` defines it.
- Design system is unchanged: Ant Design v6 default tokens, documented in `DESIGN.md`. The prototype
  uses the same antd version as `web/` (6.5.1) so tokens, component APIs and class names line up.
- The prototype's own rules (hash router, prototype strip, latency switch) are review scaffolding,
  not design decisions.

---

## 1. What was wrong with the current UI

| # | Problem | Evidence in `web/src/main.tsx` |
| --- | --- | --- |
| 1 | The landing page answers "what exists?", not "is anything broken?" — the question an operator opens the panel with. | `ProjectsScreen` is the whole landing experience. |
| 2 | Environments are `Tabs`, and Overview / Variables / Composes are `Tabs` **inside** them: two levels of the same control, so "which Environment am I in?" is easy to lose. | `ProjectDetailScreen` → nested `<Tabs>`. |
| 3 | The Compose screen gives five actions equal weight (Save / Save & Deploy / Restart / Stop / Delete) and puts the editor in a fixed 480px box. | `ComposeDetailPanel` action row. |
| 4 | Logs live in a second tab and must be loaded by hand; the tab is not linkable, so "look at the logs" means "click twice". | `loadLogs` on tab change. |
| 5 | Environment Variable values are shown in plain text in a table, and there is no way to paste a `.env` file. | `EnvironmentVariablesPanel` value column. |
| 6 | Every delete is a `Popconfirm`, including ones that remove containers, networks and volumes from the host. | `Popconfirm` on Project / Environment / Compose delete. |
| 7 | There is no way to jump to a Compose, and no breadcrumb: the Project page replaces the content with only a "Back to Projects" button. | `onBack` handlers. |
| 8 | The panel never admits its own state: no "last checked", no "unsaved changes", no "not deployed yet" — a stopped stack and a never-deployed one look identical. | `ComposeStatusTag` returns bare `Stopped` for `total === 0`. |

## 2. Principles

Ant Design's four values, applied to this panel. When two options conflict, the one that leaves the
operator more certain wins.

- **Natural** — navigation mirrors the domain and nothing else: Sider = sections, breadcrumb = where
  you are, tabs = sections of one Environment. No invented groupings.
- **Certain** — every screen states its own status: unsaved changes, last-checked time, "not deployed
  yet". Pending work shows on the control that started it.
- **Meaningful** — primary buttons are reserved for Deploy, Create and Save. Status colours carry
  meaning; decorative colour is not used.
- **Growing** — same header, same status tag, same empty-state shape from a 4-Compose Project to a
  40-Compose host. Density comes from tables, not from shrinking the grammar.

## 3. Information architecture

```
Sider (sections)          Breadcrumb (context)                          Content
─────────────────────────────────────────────────────────────────────────────────────────
Overview                  Overview                                      dashboard
Monitoring                Overview / Monitoring                         six host charts
Projects                  Overview / Projects                            cards | table
  └ my-saas               Overview / Projects / my-saas                  [environment switcher]
                                 └ Composes | Variables | Settings
  └ landing               … / my-saas / production / web                 editor + status rail
Design system             Design system / Guidelines                     this document, rendered

Routes (prototype):  #/                                      #/projects
                     #/monitoring
                     #/projects/:projectId                    #/projects/:projectId/env/:environmentId/:tab
                     #/projects/:projectId/env/:environmentId/compose/:composeId
                     #/design/guidelines  #/design/states  #/signin  #/setup
```

Rules:

- One **Environment switcher** (`Segmented`), one **tab row** (`Composes | Variables | Settings`).
  Never nest a tab row inside another tab row.
- A Compose always has its own URL and its own page. No "detail inside a tab" screens.
- The Environment stays selected while moving between tabs, so Variables → Composes does not lose
  context.
- Every screen shows a breadcrumb trail; the top-level Overview shows none, because a single crumb
  would just repeat the page title.

## 4. Screens

### 4.1 Overview (`#/`)

Host-level dashboard. Purpose: answer "is anything broken?" in one screen.

- Four stat cards: Projects, Environments, Composes, Containers running (`7 / 9`), each with a
  one-line hint underneath.
- **Needs attention** list: every Compose that is partly running, stopped after a successful deploy,
  or has an unhealthy service, with the offending service named and a single `Open` action.
- **Recent activity** timeline: who did what, to which stack, when.
- **All Composes** table: flat view across Projects with Project/Environment, container counts and
  last-edited time. This is the operator's index when they do not remember which Project holds what.

### 4.2 Monitoring (`#/monitoring`)

Host-level metrics: six charts over one selectable window. Purpose: answer "is the host in
trouble?" without ssh'ing in — and be honest about what the panel can and cannot read.

- Header: a window control (`Segmented`: 15m / 1h / 6h / 24h, default **1h**), a `Refresh`
  button (the spinner belongs to the button), and the sampling budget in one line under the
  title.
- Six charts, two per row on desktop: CPU Usage, Memory Usage, Disk Space, Docker Disk Usage,
  Block I/O (read + write), Network I/O (rx + tx).
- Percent metrics get a fixed 0–100 axis; byte rates auto-scale to a nice maximum. Hairline
  grid at 25/50/75%, x labels every 15 minutes (`HH:mm`), coarsening to 30/60 minutes only when
  a wider window would crowd them.
- Each chart carries its current value, avg / peak / low for the window, and a hover crosshair
  whose tooltip lists every series at the nearest sample. The capacity metrics (Disk, Docker)
  show `used of total` instead of avg/peak/low; Memory shows both, because the percent alone
  does not say whether the host has room.
- **≥85% is a design default** for CPU, memory and disk: the current value switches to
  `colorWarning`, never the line. A warning colour over a whole chart is alarm wallpaper.
- A freshly restarted panel has only the minutes since it started: the chart says
  `collecting — 187 of 360 minutes` and plots what exists, instead of a half-empty axis.
- A source the panel cannot read renders the `EmptyState`, never a zeroed line — and never the
  container's own counters dressed up as host numbers.
- **Host metrics only.** One container of one Compose over the same four windows is the Compose
  screen's **Monitor** tab (§4.6) — the host screen answers "is the host in trouble?", the Monitor
  tab answers "what has this service been doing?", and a deploy spike there can be read against the
  deploy that caused it.
- The page scrolls with the document; the charts are fixed-height boxes. No nested scrollers.

#### Sampling budget

- **10s** inside the hour (360 samples/hour in an in-process ring buffer, ~37 KB), **1-minute
  rollups** for the rest of the day (~150 KB). 15m and 1h read the 10s ring; 6h and 24h read the
  rollups. The ring is memory only; the rollups are rows in the panel's existing SQLite, so a
  restart costs the current hour's detail, not the day's shape.
- **Per container**, the same budget at a coarser cadence: **30s** inside the hour and 1-minute
  rollups beyond it — a container is a `docker stats` read per service, not four `/proc` files, and
  a service's shape is read in minutes. At ~**72 bytes per sample** that is ~**170 KB for the hour
  across 20 containers**, and ~**2 MB/day** of rollups in the same SQLite. Detail in the hour, the
  day in rollups, keyed by (Compose, service) rather than by container.
- Reading the sources costs under **10 ms** and about **7 KB** of text per sample. No extra
  process, no agent, no sidecar, no new dependency: the panel reads them itself.
- Already host-accurate from inside the panel container (Docker socket + data dir only): CPU
  (`/proc/stat` — jiffies are host-global), memory (`/proc/meminfo`, host-global only because
  Docker here does not mount lxcfs — runtime behaviour, not a contract), block I/O
  (`/proc/diskstats` — the same 28 devices inside and outside), and disk space (`statfs` on the
  mounted data dir, which is a host filesystem).
- Network is the gap, and it fails silently rather than loudly: the container's `/proc/net/dev`
  lists the same interface names as the host (`lo`, `eth0`) with container-scoped counters
  (measured: 200 B rx inside vs 5,273,301,899 B rx on the host). Mounting host `/proc`
  read-only at `/host/proc` does not fix it — `/host/proc/net/dev` is `/proc/self/net/dev`, and
  `self` is the process doing the reading. Read `/host/proc/1/net/dev` instead: PID 1's network
  namespace is the host's, and the numbers match.
- `docker system df` costs **0.64–0.68 s** and ~32 MB of transient RSS per call, so it is cached
  and refreshed every few minutes, never on the 10s tick. `docker stats --no-stream` costs
  2.03 s and must not be the source at all.
- A restart costs only the current hour at 10s resolution: the ring buffer is memory, the 1-minute
  rollups are rows in the panel's SQLite, so the window rebuilds from them — and the `collecting`
  state above is what the seam looks like. For scale: the panel binary idles at ~3.9 MB RSS, so the
  buffers are about a 5% addition.

### 4.3 Projects (`#/projects`)

- Filter by name/description, sort (recently updated / name / size), and a **Cards ⇄ Table** toggle —
  cards for a handful of Projects, the table once the list is long.
- Every Project card/row carries the status rollup (`5/6 · Partly running`) so "which app is broken"
  never requires drilling in.
- Row menu: Open, Edit details, Delete.

### 4.4 Project (`#/projects/:projectId`)

- Header: name, status rollup, Environment and Compose counts, description, `Edit details`.
- Environment switcher with per-Environment Compose counts; the active Environment's container
  rollup sits on the right.
- **Composes** tab: table of Compose, Containers, Deploy identity, Last edited, with per-row
  `Open`, a one-click `Deploy`/`Stop`, and a `⋮` menu (Deploy / Restart / Stop / Delete).
- **Variables** tab: see 4.5.
- **Settings** tab: Project details form, Environment list with delete, and a danger zone for
  deleting the Project.

### 4.5 Environment Variables (Project → Variables)

- Values are **masked by default** (`••••••••`) with reveal and copy buttons; a `secret` / `plain`
  tag marks intent.
- Key filter, `New Variable` modal (key validation: `[A-Za-z_][A-Za-z0-9_]*`).
- **Paste a `.env` file**: textarea, live parse preview, duplicate-key error, and an explicit
  "replace N variables with M" confirm — because operators have the file already.
- Standing note: changes apply on the next deploy of each Compose in the Environment.

### 4.6 Compose (`…/compose/:composeId`)

The core screen. Two tabs, in the path so both are linkable and the back button works:
**Editor** (`…/compose/:composeId`, the default) and **Monitor** (`…/compose/:composeId/monitor`).
An unknown segment falls back to Editor rather than a not-found page. Layout of the Editor tab:
editor on the left, status rail on the right, both in normal document
flow — the page scrolls as one, no nested scrollers. The two columns stretch to the same height, so
the editor is exactly as tall as the status rail beside it, with `LAYOUT.editorMinHeight`
(`clamp(640px, 80vh, 1000px)`) as the floor for when the rail is short. Monaco needs a bounded
height; it gets one from the rail rather than from a hard-coded number.

- **Header**: name, status tag, `Unsaved changes` tag when dirty; actions graded by weight —
  `Logs`, `⋮` (Restart / Stop / Delete), `Save`, and one primary `Deploy` that becomes
  `Save & Deploy` while the editor is dirty.
- **Editor**: `docker-compose.yml` label, `Modified` marker, `Discard`, a full screen button, and a banner
  offering Save/Discard while dirty.
- **Full screen editor**: the icon-only button in the editor header opens the same file — one Monaco
  model, shared by `path`, so the two editors are the same document rather than a synced copy — in a
  true full screen modal. A 56px toolbar carries the filename, the `project / environment / compose`
  trail, the status tag, `Modified`, `Discard`, `Save` and a close button, plus the standing line that
  edits are shared with the page and closing does not discard them. The YAML fills the rest; the right
  rail (340px, hairline divider) lists the Environment's variables with the same masking, reveal, copy
  and `secret` / `plain` treatment as §4.5. Whitespace matters here more than anywhere else, so the
  variables panel is the shortest path from "I need a value" to "the file references it": the filter
  narrows by key, and **clicking a row or `Insert` drops the literal `${KEY}` at the cursor**,
  replacing the selection if there is one — the reference, never the value, because variables are
  injected at deploy time. The insertion is its own feedback (no toast per insert), a row already
  referenced in the draft carries a small success-coloured check, and an Environment with no variables
  gets the `EmptyState` with a `Manage` link to the Variables tab. Esc and the close button close the
  modal; the draft survives both, and the modal closes itself if the Compose changes underneath it.
- **Status rail**: overall tag + "Checked 3 minutes ago"; per-service rows (name, health dot, state
  tag, image, ports); a warning alert when something is down; `Deploy now` when nothing has ever run.
- **Deploy identity**: the on-disk path and the `docker compose -p` name, both copyable, with the
  ADR-0005 note that identity is frozen at creation.
- **Environment**: which Environment supplies variables, count, and how to reference them.
- **Activity**: this Compose's recent actions.
- **Logs drawer**: service filter, line count, copy; `Follow` is visible but disabled, with a tooltip
  explaining that streaming is out of v1 scope.

#### Monitor tab (`…/compose/:composeId/monitor`)

One service of this Compose, over the same 15m / 1h / 6h / 24h windows as the host screen, on the
same `MetricChart` and the same control family. The header, `Logs`, `Save`/`Deploy` and the status
tag stay above the tabs, because they belong to the Compose rather than to either tab.

- **Controls row**: one `Select` for the service (name, image, and the status-rail dot vocabulary),
  the window `Segmented` (default **1h**), and `Refresh` with the spinner on the button.
- **Facts strip**: image, short container ID (copyable), state and health, PIDs, and when the
  Compose was last deployed — built from `ServiceStateTag`, `HealthBadge`, `CodeValue` and
  `RelativeTime`, not from new markup. It carries the identity rule in one line: **the container ID
  changes on every deploy, the service name does not** — which is why history is keyed by
  (Compose, service) and the charts survive a redeploy.
- **Four charts**, 2×2, fixed height: **CPU** (`percent`, axis `auto` — a container can exceed one
  core, and a fixed 0–100 axis would clip 150%), **Memory** (`percent` of the container's own limit
  on a 0–100 axis, with the absolute reading as "412 MB of 2 GB used"; usage **minus page cache**,
  because cache is the host's memory and subtracting it is what makes the number agree with
  `docker stats`), **Network I/O** (received / sent) and **Block I/O** (read / write) as byte rates.
- **Deploy marker**: a dashed vertical line labelled `Deployed` at the Compose's last deploy time,
  drawn on all four charts when it falls inside the selected window — a spike is read against the
  deploy that caused it.
- **States**: a Compose that has never been deployed, or has no services, gets the `EmptyState`
  pointing at the Editor tab, because deploying is what starts collection; a service with no history
  in the window renders `partial`; a service that is not running now keeps the history it has and
  simply stops drawing at the point it stopped.
- The page scrolls with the document; the charts are fixed-height boxes. No nested scrollers.

### 4.7 Auth (`#/signin`, `#/setup`)

Split layout: what the panel is on the left (in `colorPrimaryBg`), the form on the right. Setup is a
two-step flow (Account → Ready) ending in a `Result` with the Project → Environment → Compose
mental model restated.

### 4.8 Design system (`#/design/guidelines`, `#/design/states`)

The guideline and the state gallery are part of the prototype so reviewers read the rules next to the
screens. The states gallery covers loading, empty (both "nothing yet" and "filtered to nothing"),
inline and page-level errors, form validation, outcome feedback, destructive confirmations and narrow
viewports.

## 5. Status vocabulary

One vocabulary, everywhere a Compose state appears. Derived entirely from the API's
`running` / `total` counts — no new backend field.

| State | Condition | Rendering | Notes |
| --- | --- | --- | --- |
| Running | `running = total > 0` | filled `success` tag, `2/2 · Running` | |
| Partly running | `0 < running < total` | filled `warning` tag, `1/2 · Partly running` | Raises the Overview attention list |
| Stopped | `running = 0`, previously deployed | filled `default` tag, `0/1 · Stopped` | Distinct from never deployed |
| Not deployed | `total = 0`, never deployed | **dashed outline** tag, `Not deployed` | Must not read as "healthy but empty" |

- In dense table cells use the dot form (`● Running 2/2`) instead of a filled tag.
- Service-level states in the rail: `running` (success), `exited` (error), `restarting` (warning),
  `created` (default). Health is a separate dot: healthy / unhealthy / starting.
- Never colour a whole row by status. Colour the dot, the tag or the count.

## 6. Layout and spacing grammar

- Two bars: 56px header (`Layout.headerHeight`) and a 248px sider. Content is a 24px-padded,
  1280px-max column.
- The header's right cluster (search / refresh / theme / account) is pushed out by
  `marginInlineStart: 'auto'`, not by `justify="space-between"` alone. The breadcrumb is hidden on
  routes with a single crumb, and the cluster would then be that Flex's only child — which
  `space-between` parks at the start. The cluster's right edge must land on the page gutter (24px),
  i.e. directly under the prototype strip's segmented control.
- All gaps come from the antd spacing scale (8 / 16 / 24). No magic numbers.
- Surfaces come from tokens only: page = `colorBgLayout`, cards = `colorBgContainer`, inset blocks =
  `colorFillQuaternary`. No hex values in screen code.
- One `PageHeader` per screen: title + tags, secondary description, right-aligned actions, hairline
  bottom border.
- Cards group; they do not decorate. A card holding one line of text should not be a card.
- Editor screens are the exception to max-width: the Compose screen uses the whole 1280px column,
  with the editor stretched to the rail's height — `LAYOUT.editorMinHeight`
  (`clamp(640px, 80vh, 1000px)`) is the floor, so the file stays readable when the rail is short.
  Do not pass `align="top"` to that `Row`: stretching is what makes the match work.
- No nested scrollers. Pages scroll with the document; the only bounded boxes are the editor and
  the logs viewer, where a bounded box *is* the feature. Inside the full screen editor the modal itself
  is the bounded box: it states its own height (`100vh`) and nothing in it scrolls the page — the one
  place that scrolls internally is the fixed 340px variables column beside the file, which has no
  height of its own to grow into.
- Tables over card grids past ~12 items; the Projects screen ships both and lets the operator choose.
- Controls stay 32px tall (DESIGN.md); only in-table actions drop to `size="small"`.
- Responsive rules: below 992px the sider collapses to zero, page actions wrap under the title,
  tables scroll horizontally, and the Compose rail stacks under the editor.

## 7. Component patterns

Reusable pieces live in `web-design/src/components/`; port them into `web/src/` as-is.

| Pattern | File | Rule |
| --- | --- | --- |
| `PageHeader` | `components/primitives.tsx` | Every screen opens with exactly one. |
| `StatCard` | `components/primitives.tsx` | Label, value, 12px hint, optional icon chip. Four per row. |
| `ComposeStatusTag` / `ComposeStatusInline` | `components/primitives.tsx` | Tag form for headers/cards, dot form for tables. |
| `EmptyState` | `components/primitives.tsx` | Glyph + title + one-line explanation + the action that resolves it. |
| `MetricChart` | `components/MetricChart.tsx` | Fixed-height inline SVG, no chart dependency: 0–100 axis for percent (`axis="auto"` takes the maximum from the data instead, for a container that can exceed one core), nice-max for byte rates, hairline grid, crosshair tooltip, min/max downsampling to the plot width, and optional `markers` — dashed vertical references such as `Deployed`. Used by both Monitoring and the Compose screen's Monitor tab. `partial` and `unavailable` are first-class states. |
| `ServiceMonitor` | `components/ServiceMonitor.tsx` | The Compose screen's Monitor tab body: service `Select`, window `Segmented`, facts strip, four `MetricChart`s and the deploy marker. History is keyed by (Compose, service). |
| `DangerModal` | `components/DangerModal.tsx` | Type-the-name confirmation, lists what will be destroyed. |
| `VariablesPanel` | `components/VariablesPanel.tsx` | Masking, reveal/copy, bulk `.env` paste. |
| `YamlEditor` | `components/YamlEditor.tsx` | One editor surface; `path` keeps a Monaco model per Compose. Two editors on one `path` share that model — the second must set `keepCurrentModel`. Takes its theme from `ThemeModeContext`: Monaco themes itself, so it cannot read antd tokens off the React tree. |
| `FullscreenEditor` | `components/FullscreenEditor.tsx` | Full screen Compose editor: page-owned draft, one shared Monaco model, variables rail that inserts `${KEY}` at the cursor. |
| `CommandPalette` | `components/CommandPalette.tsx` | ⌘K over Projects and Composes. |
| `AppShell` | `app/AppShell.tsx` | Sider + header + breadcrumb + attention badge. |
| Theme overrides | `app/theme.ts` | Layout, Menu, Table, Card component tokens + the status vocabulary. |

## 8. Interaction and feedback rules

- Loading belongs to the control that started it: button spinners for actions, skeletons for first
  paint, table `loading` for refetches. No full-page overlays after first paint.
- Outcomes use `App.useApp()` messages, one line, naming the thing: "Deployed web". Modals are for
  decisions, messages for outcomes.
- Multi-step work (deploy, bulk variable replace) uses a **keyed notification** so progress is
  replaced by the result rather than stacked on top of it.
- Errors stay on their screen as an `Alert` with a `Retry` action. No toast that vanishes before it is
  read.
- Status changes only after the call resolves — the UI never predicts the host's state.
- Unsaved YAML is always visible: header tag, `Modified` marker, banner with Save/Discard. Deploy
  saves first and says so.
- Destructive actions: `Popconfirm` for cheap deletes (one variable), typed-name modal for anything
  removing containers, volumes or files.
- Filters that match nothing are not empty states: keep the filter, say what it is filtering.

## 9. Content rules

- Use the domain vocabulary from `CONTEXT.md`: Project, Environment, Compose, Environment Variable.
  Never "stack", "app", "job", "record".
- Copy names consequences: "removes containers, networks and volumes, and deletes its files under
  `dataDir/my-saas/production/web/`".
- Where the panel cannot do something (log streaming, log download, dark mode), say so in a tooltip
  instead of hiding the affordance. Visible-but-disabled is honest; invisible is confusing.
- Second person, present tense, sentence case. No exclamation marks.

## 10. Accessibility

- Status is never colour-only: tag text carries the meaning, colour reinforces it.
- Icon-only buttons carry `aria-label` (refresh, reveal value, row actions).
- ⌘K opens search, Esc closes overlays, focus rings come from antd and are never suppressed.
- The default `#1677FF` primary on white is below WCAG AA for small text (noted in `DESIGN.md`). If
  strict AA is required, darken `colorPrimary` in `ConfigProvider` rather than restyling screens.
- Destructive confirmations state the exact object name and require it to be typed.

## 11. API gaps

Design decisions that need something the Go API does not return yet. Each degrades gracefully today.

| Design element | API today | Needed |
| --- | --- | --- |
| Per-service rows in the status rail | `status.running` / `status.total` only | A `services` array (name, image, state, health). `docker compose ps --format json` already carries it. |
| Per-service metrics on the Compose screen | Nothing — no per-container series of any kind | The `services` array above plus the **service → container ID mapping** from `docker compose ps --format json`, and a series per service over the four windows. History is keyed by **(Compose, service)**, never by container ID: a redeploy mints a new container, and a history keyed by container would restart at zero on the very deploy the operator is trying to read. |
| Container CPU | Nothing | Percent of one core, **not clamped at 100**: a container that uses two cores reads 150%, and the chart's axis has to come from the data (the host chart's fixed 0–100 axis would clip it). |
| Container memory | Nothing | Usage **minus page cache**, against the container's own limit. Page cache is the host's memory rather than the service's footprint, and leaving it in is what makes the number disagree with `docker stats`. |
| "Checked N minutes ago" | No timestamp on the status read | Return the read time, or stamp it client-side (the prototype stamps it). |
| Activity feed | Nothing recorded | An events table, or drop the card — v1 can ship without it. |
| `secret` marking on variables | Plain values, no flag | A boolean per variable so masking is deliberate rather than guessed by key name. |
| Attention count in the header | Derivable | Nothing — computed client-side from the composes list. |
| Log service filter | Whole-stack logs | Optional `?service=` pass-through to `docker compose logs`. |
| Network I/O counters | The container's own `/proc/net/dev` reports the **same interface names** as the host with its own counters (measured: 200 B rx inside vs 5,273,301,899 B rx on the host). Mounting host `/proc` at `/host/proc` is not enough: `/host/proc/net/dev` is `/proc/self/net/dev`, and `self` is the reader. | A read-only bind-mount of host `/proc` at `/host/proc`, and read network counters from `/host/proc/1/net/dev` (PID 1's network namespace is the host's — verified against the host's numbers). Until then the chart is `unavailable`, never the container's numbers. |
| Docker disk usage | Nothing. `docker system df` costs 0.64–0.68 s and ~32 MB of transient RSS per call; `docker stats --no-stream` costs 2.03 s. | Cache the `docker system df` result and refresh it every few minutes; serve the chart from that cache rather than from the 10s tick. |
| Metric history across restarts | Nothing | Decided: 10s samples live in an in-process ring buffer (lost on restart), 1-minute rollups are persisted in the panel's existing SQLite so the day survives. `collecting — N of M minutes` covers the seam. |
| A resident metrics service (cAdvisor and friends) | Nothing — and the panel should not add one | Measured on this host: cAdvisor idles at 25→33 MB RSS and climbing, needs `--privileged` and five host mounts, and still keeps only ~2 minutes of history — so it would be scraped and stored by the panel anyway. Reading four `/proc` files in-process costs ~7 KB and no new process. |
| CPU, memory, block I/O, disk space sources | Already host-accurate from inside the container (see §4.2, sampling budget). | No API change. Do not build on memory staying host-global: that is Docker runtime behaviour (no lxcfs mount), not a contract. |

## 12. Implementing this in `web/`

Order matters; each step is reviewable on its own.

1. **Port the theme** (`web-design/src/app/theme.ts` → `web/src/theme.ts`): seed overrides plus the
   Layout / Menu / Table / Card component tokens. This alone makes every existing screen feel closer.
2. **Port the primitives** (`components/primitives.tsx`, `DangerModal.tsx`) and switch the existing
   screens onto `PageHeader` and the status vocabulary.
3. **Add the shell**: sider + header + breadcrumb. Routing library choice is open; the prototype's
   hash router exists only so every screen is linkable, and its paths (`app/router.ts`) are the
   contract to preserve.
4. **Reshape Project**: Environment `Segmented` + `Composes | Variables | Settings` tabs, replacing
   the nested tabs.
5. **Rebuild Compose** as its own route: `Row`/`Col` editor + status rail + logs drawer, with the
   `Editor | Monitor` tab row in the path (`…/compose/:composeId/monitor`). The Monitor tab is a
   second consumer of `MetricChart` and of a `loadServiceMetrics(composeId, serviceName, window)`
   data-layer method, so it can land after Monitoring does.
6. **Variables**: masking, reveal/copy, bulk `.env` paste.
7. **Overview**: needs a flat "all composes" fetch (loop over Projects/Environments as the mock does)
   plus the derived attention list.
8. **Monitoring**: `PageHeader` + window `Segmented` + six `MetricChart`s over a
   `loadMetrics(window)` data-layer method. The `/proc` and `docker system df` work in §11 is the
   prerequisite for real numbers; until it lands, `unavailable` is the honest state for a source
   that is not readable, and the mock's generator shows the shape the chart expects.
9. **Optional**: ⌘K palette, dark mode (one algorithm swap for antd, plus the Monaco theme pair —
   see `monacoThemeData`, and remember Monaco rejects `rgba()` colours), Overview activity feed once
   the API records events.

Data-layer note: the mock store (`web-design/src/data/store.tsx`) is shaped like a data layer — every
method body is the stand-in for a `fetch`. Replacing the bodies with real calls to the existing
endpoints does not change any component. Do not port the prototype strip, the latency switch or the
hash router.
