# Relist Panel — UX/UI redesign guidelines

Status: **agreed and implemented in `web/`** (the port is `.scratch/design-port/`, closed). This
file is the design contract the port was held to; the rules below still govern the panel's UI.

> **Where the mock went — 2026-09-27.** These guidelines were reviewed through a clickable,
> API-free mock in `web-design/` (`pnpm dev`). With the port closed the mock left the working tree:
> its archive is the git tag `design-mock`, and its 105 states survive as
> [`design-states.md`](./design-states.md). Section numbers are unchanged — `web/src/` cites them
> (`§4.1`, `§11`, `§12`) — and the sections that describe the mock's own review surfaces (§4.9's
> board and galleries, §12's port order) now read as history.

Scope of this pass: information architecture, screen layouts, state coverage, copy, and the
interaction rules that hold them together. Out of scope: backend changes, routing-library choice,
dark mode as a shipped feature (it is wired here only to prove the tokens hold).

- Domain model is unchanged: Project → Environment → Compose (+ Environment Variables, Compose Variables, Variable Sets, which
  belong to their Compose, ADR-0015), exactly as `CONTEXT.md` defines it.
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

Routes (history URLs, as shipped in `web/`):  /                                      /projects
                     /monitoring
                     /projects/:projectId                    /projects/:projectId/env/:environmentId/:tab
                     /projects/:projectId/env/:environmentId/compose/:composeId
                     /projects/:projectId/env/:environmentId/compose/:composeId/monitor
                     /signin
                     Design-system screens stay prototype-only: #/design/guidelines, #/design/states
```

An admin's sider groups the host-wide pages under titles (antd `Menu` `type: 'group'`), each
answering one question. A group is a title, not a page: it has no route and no breadcrumb crumb.

| Group | Question | Items |
|---|---|---|
| *(none)* | How is my work doing? | Overview, Projects, Deployments |
| Host | How is the machine doing? | Monitoring, Docker |
| Build & images | Where do images come from? | Images, Registry, Builders, SSH keys |
| Routing | How does traffic get in? | Traefik, Certificates, Basic auth |
| Security | What is exposed, and who did what? | Vulnerabilities, Audit log |
| Panel | The panel itself | Users, Panel update |
| Recent Projects | — | the five newest Projects |

A member sees only Projects and Deployments, then Recent Projects.

Rules:

- One **Environment switcher** (`Segmented`), one **tab row** (`Composes | Variables | Settings`).
  Never nest a tab row inside another tab row.
- A Compose always has its own URL and its own page. No "detail inside a tab" screens.
- The Environment stays selected while moving between tabs, so Variables → Composes does not lose
  context.
- Every screen shows a breadcrumb trail; the top-level Overview shows none, because a single crumb
  would just repeat the page title.

## 4. Screens

### 4.1 Overview (`/`)

Host-level dashboard. Purpose: answer "is anything broken?" in one screen.

- Four stat cards: Projects, Environments, Composes, Containers running (`7 / 9`), each with a
  one-line hint underneath.
- **Needs attention** list: every Compose that is partly running, stopped after a successful deploy,
  or has an unhealthy service, with the offending service named and a single `Open` action.
- **Recent activity** timeline: who did what, to which Compose, when.
- **All Composes** table: flat view across Projects with Project/Environment, container counts and
  last-edited time. This is the operator's index when they do not remember which Project holds what.

### 4.2 Monitoring (`/monitoring`)

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

### 4.3 Projects (`/projects`)

- Filter by name/description, sort (recently updated / name / size), and a **Cards ⇄ Table** toggle —
  cards for a handful of Projects, the table once the list is long.
- Every Project card/row carries the status rollup (`5/6 · Partly running`) so "which app is broken"
  never requires drilling in.
- Row menu: Open, Edit details, Delete.

### 4.4 Project (`/projects/:projectId`)

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
- **Paste a `.env` file**: textarea, live parse preview (each row tagged `new` / `changed` /
  unchanged), duplicate-key error — because operators have the file already. By default a paste
  **merges**: new keys are added, changed values updated, unchanged rows not written, and keys the
  paste drops are kept; the OK button says "Apply N changes" and is disabled at zero. A **Replace
  all** checkbox, off each time the modal opens, also removes the keys the paste drops; the confirm
  then reads "Replace N variables with M" (danger when it removes something). A `.env` carries no
  secret flag, so a key the list already has keeps its own, and a new key takes the create form's
  guess from its name: a re-paste never unmasks a secret.
- Standing note: changes apply on the next deploy of each Compose in the Environment.
- No Variable Sets card: sets belong to a Compose and are managed only on the Compose's Variables tab (ADR-0015).

### 4.6 Compose (`…/compose/:composeId`)

The core screen. Tabs in the path so each is linkable and the back button works:
**Editor** (`…/compose/:composeId`, the default), **Variables** (`…/compose/:composeId/variables`), **Domains** (`…/compose/:composeId/domains`), **Logs** (`…/compose/:composeId/logs`), **Terminal**
(`…/compose/:composeId/terminal`) and **Monitor** (`…/compose/:composeId/monitor`).
An unknown segment falls back to Editor rather than a not-found page. Layout of the Editor tab:
editor on the left, status rail on the right, both in normal document
flow — the page scrolls as one. The editor sizes itself to its content (Monaco's content height),
clamped between `LAYOUT.editorAutoMinHeight` (320px) and `LAYOUT.editorAutoMaxHeight` (`80vh`);
past the cap it scrolls inside, and the mouse wheel passes through to the page at its edges. The
rail keeps its own height beside it.

**Variables tab** has three small cards, in this order. Each card's explanation sits in an ⓘ tooltip beside
its title, and none repeats "next deploy": the banner below says it once. The two variable lists are the §4.5
editor in its **bare** form: the table alone (Key, Value, row actions; no `secret` / `plain` tag, the mask and its
eye already tell), row actions shown on hover or keyboard focus and always on touch devices, "+ Add" and
**Paste .env** as links under the table (the §4.5 paste, with its flag rule), and the key filter only above ten rows.

1. **Compose variables**: the Compose's own variables (add, edit, delete, secret flag, reveal, copy, paste). A row
whose key is also an Environment Variable carries an `overrides <environment>` tag on the same line; its tooltip
shows the Environment value, masked as `••••` when that Environment Variable is secret. The Editor tab's
Environment card counts distinct keys across both.
2. **Variable Sets**: the Compose's named sets (ADR-0015), one `line` tab per set labelled with its name and entry
count. **New set** sits in the card header (the empty state's button alone while there are none); a new set's
tab opens once it is created. Each pane starts with a line naming the Services that bind the set ("Bound to db,
worker" or "Not bound to any Service yet") with **Rename** (a modal) and **Delete** beside it, then the bare
editor for its entries. Binding happens only in the next card. A set is created, renamed and deleted here only.
Deleting a set shows a `Popconfirm` naming the bound Services ("Unbinds it from api, worker. Takes effect on the
next deploy.") and removes its bindings with it; an unbound set's confirmation reads "Its entries are deleted
with it."
3. **Service bindings**: a table with one row per Service in the saved YAML (the list the Domains form uses) and a
borderless multi-select of this Compose's Variable Sets (ADR-0015); tag order is binding order and a later set
wins. Bindings for a Service the YAML no longer declares are listed after them with a warning icon and can only be
removed. Each Service row's icon button (⚡, "Effective env for <service>") opens an **Effective env** modal: the
Service's winning value per key, its source (Environment, Compose, or a set), the layers it shadows, and the
YAML's own `environment:` keys as `defined in the YAML` rows, with an `overridden by YAML` tag on a merged row
whose key the YAML also sets. Values follow the §4.5 masking.

The "changes apply on the next deploy" banner, shared with Domains, shows when a Compose Variable, a domain, a
binding, or an entry of a bound Variable Set changed since the last deploy (a set rename does not count). Every write
in the Variable Sets and bindings cards reloads the tab, so the banner shows without a page reload.

**Domains tab**: a table of the Compose's domains (host + path as a link, `service:port`, an HTTPS tag, Edit and
Delete) with an **Add domain** button in the header and an `EmptyState` when there are none. Add and Edit share one
modal form. Delete is a `Popconfirm` naming the Address, not a `DangerModal`: nothing on the host changes until the next
deploy. An `info` `Alert` with a **Deploy** button, "Domain changes apply on the next deploy.", shows when the Compose
has deployed before and `domainsChangedAt >= lastDeployedAt`. Above the tabs, a deploy's `warnings` show in a closable
`warning` `Alert` until it is closed or the next deploy replaces it.

- **Header**: name, status tag, `Unsaved changes` tag when dirty; actions graded by weight —
  `⋮` (Restart / Stop / Delete), `Save`, and one primary `Deploy` that becomes
  `Save & Deploy` while the editor is dirty.
- **Editor**: `docker-compose.yml` label, `Modified` marker, `Preview`, `Discard`, a full screen button, and a banner
  offering Save/Discard while dirty. Below the `sm` breakpoint `Discard` and `Preview` drop their text and keep
  icon, tooltip and aria-label, so the full screen button stays on the header.
- **Preview**: a read-only modal with the file a deploy would write for the current draft — the environment
  file, the Domains tab's Traefik labels, and relist-network with its `.internal` aliases where needed; `${KEY}` left as references, never values.
  It loads once per opening, says when the draft is unsaved, shows the deploy's warnings as `warning` alerts
  and a refusal as an inline `error` alert (not a toast), and copies the whole file from the title. Its
  Monaco model has its own `path`, never the editor's.
- **Full screen editor**: the icon-only button in the editor header opens the same file — one Monaco
  model, shared by `path`, so the two editors are the same document rather than a synced copy — in a
  true full screen modal. A 56px toolbar carries the filename, the `project / environment / compose`
  trail, the status tag, `Modified`, `Discard`, `Save` and a close button, plus the standing line that
  edits are shared with the page and closing does not discard them. The YAML fills the rest; the right
  rail (340px, hairline divider) lists the variables a deploy writes — the Environment's, with this Compose's own laid over them (a Compose Variable wins its key and carries a `compose` tag) — with the same masking, reveal, copy
  and `secret` / `plain` treatment as §4.5. Whitespace matters here more than anywhere else, so the
  variables panel is the shortest path from "I need a value" to "the file references it": the filter
  narrows by key, and **clicking a row or `Insert` drops the literal `${KEY}` at the cursor**,
  replacing the selection if there is one — the reference, never the value, because variables are
  injected at deploy time. The insertion is its own feedback (no toast per insert), a row already
  referenced in the draft carries a small success-coloured check, and with none at either layer the rail
  gets the `EmptyState` with a `Manage` link to the Environment's Variables tab; while the Compose Variables
  load, or if they fail (a warning), it does not claim an empty or complete list. Esc and the close button close the
  modal; the draft survives both, and the modal closes itself if the Compose changes underneath it.
- **Status rail**: overall tag + "Checked 3 minutes ago"; per-service rows (name, health dot, state
  tag, image, ports); a warning alert when something is down; `Deploy now` when nothing has ever run.
- **Deploy identity**: the on-disk path and the `docker compose -p` name, both copyable, with the
  ADR-0005 note that identity is frozen at creation; then the stable Internal hostname pattern
  `<service>.<project name>.internal` (not copyable — it has a placeholder) and one line on which
  Services get it, pointing at Preview for each Service's exact aliases (ADR-0014).
- **Environment**: which Environment supplies variables, count, and how to reference them.
- **Activity**: this Compose's recent actions.
- **Logs tab** (read on open, not a drawer): service filter, line count, copy; `Follow` is visible but disabled, with a tooltip
  explaining that streaming is out of v1 scope.
- **Deploy with fresh volumes**: the typed-name confirmation displays a warning
  that named volume data is permanently deleted before deployment and is not
  restored if deployment fails. It directs the operator to back up first. A
  failed Deployment retains a warning in its log Drawer when the backend confirms
  named volumes were actually removed; the option alone is not evidence of removal.

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

### 4.7 Volume files (Compose → Volumes, and the Volume Explorer)

Opened from the Compose page, which gains a third tab beside Editor and Monitor. The tab lists only the Volumes
**that Compose** mounts — never the host's — and is a launcher, not a file manager. The files are read in the Volume
Explorer, a route of its own opened in a new browser tab, because browsing a directory is somewhere you go rather than
a panel you glance at. It keeps the panel's chrome and has no nav entry of its own: a Volume is meaningless without
its Compose as context.

Every operation is one `docker run --rm` of the helper from the panel's own image (ADR-0010), mounting exactly one
source. Nothing stands between operations — no handle to open, keep alive or expire, and nothing mounted while idle.
Read operations mount the source `:ro`; `write`, `delete` and `extract` mount it `:rw`, and the mount flag — not the
UI — is the enforcer.

- **Where the list comes from.** `docker compose config --format json`, never a hand-rolled YAML parse. The panel
  never accepts a host path from a form: a Volume can only be one the Compose already declares. This is the invariant
  the whole feature's privilege rests on, and it is why the tab has an unavailable state — the CLI fails with
  `couldn't find env file` until the Compose has been deployed once.
- **tar.gz is the only archive format, in both directions.** Download streams the helper's stdout straight to the
  browser, restore streams the upload straight into `extract`: nothing is staged on the panel's disk. Zip is out —
  a zip restore would need random access, and a download-only format would have no restore story.
- **The operation user is confirmed, not inferred.** The Explorer resolves the `--user` identity — a mounting
  Service's resolved `user:`, or a numeric uid:gid — behind an explicit Confirm, and nothing is listed, read or
  written until it is pressed; root and an unverifiable bind each need their own acknowledgement first. A Service
  whose resolved `user:` is not itself a numeric uid:gid cannot be confirmed: `--user` resolves a name against the
  helper image, not the Service's own — the card says so and asks for the explicit numeric override. A change to
  the form keeps the confirmed snapshot in force until Confirm is pressed again. The Volumes tab only launches the
  Explorer; it starts no archive itself.
- **Creating entries is one operation per dialog.** New folder (`mkdir`) and New file (`create`) are one dialog
  with two titles, created as the confirmed operation user, one level at a time, and owned by that uid:gid.
  `create` is O_EXCL, so an existing name is refused — in the dialog against the listing, and with a 409 when it
  still races — never replaced the way a truncating upload would.
- **Restore discloses partial extraction.** Every entry is validated before it is written, but there is no
  transaction across a stream: entries written before a failure stay written, and the confirmation says so before
  the operator starts.
- **The caps are hard-coded.** 1 GiB per upload, 1 MB for the editor, a 10-minute archive timeout; a restore is
  capped at 1 GiB compressed, 4 GiB uncompressed and 10,000 entries. An upload beyond the Volume's free space is
  refused before anything is read — the helper checks `statfs` — and a stream that still overruns loses its partial
  file.
- **A symlink is shown, described, and followed only when it stays inside the Volume.** Listed with its target, and
  marked when it leaves. Hiding one would misrepresent the data: an operator who cannot see it cannot understand why
  a path exists. Only the traversal into it is refused, and `os.Root` refuses it too — the Explorer is not stricter
  than the kernel, it is honest about the boundary.
- **Errors carry the helper's own words.** A permission failure names the uid the operation runs as — the Service's
  resolved `user:`, else `0:0` with a warning that files it writes become root-owned — because that is the one part
  of it the operator can change.
- **The two pre-flight warnings.** An unverifiable bind source — an absolute path outside the data directory, where
  looking at it without mounting it is impossible and mounting it is what would create it as `root:root 0755` — waits
  for one acknowledgement per visit, before its first operation. A Compose whose Services are running gets a banner
  naming what a write can corrupt, and it lets the write proceed. See §8.
- The tab's empty state says the Compose declares no volumes rather than showing an empty table.
- The page scrolls with the document, like every other screen; the file editor is the only bounded box (see §6).

### 4.8 Auth (`/signin`)

Split layout: what the panel is on the left (in `colorPrimaryBg`), the form on the right. There is
no setup screen: the admin is made with `relist-panel create-admin` in the panel container, and
until then sign-in refuses everyone like a wrong password, with no hint or link. When the account
has two-factor on, sign-in asks for the code as a second step: an autofocused six-digit
`Input.OTP` replaces the password form, a complete code submits, and **Back** returns to the
password step and clears it.

**Migration lock.** While an old host is locked for a Migration, a signed-in admin sees one centred
card instead of the app. Locked, it shows *This server is being migrated*, who is copying it, and the
lock's steps as vertical `Steps`; Migrated, a success `Result` titled *Migrated to* the target (a link
only when it is `http(s)://`) with when, and what is left to do. It has no action buttons: it names
`docker compose exec panel relist-panel migration resume` as copyable code with what resuming does,
and leaves by itself once the lock is lifted.

### 4.9 Design system (`#/design/flow`, `#/design/board`, `#/design/map`, `#/design/guidelines`, `#/design/states`)

*Retired with the mock: this section describes the design-system surfaces the mock hosted, not
routes the panel ships. The rules in the rest of this file are what the port kept.*

The guideline and the state gallery are part of the prototype so reviewers read the rules next to the
screens. The states gallery covers loading, empty (both "nothing yet" and "filtered to nothing"),
inline and page-level errors, form validation, outcome feedback, destructive confirmations and narrow
viewports — on the board each of those sections is also a frame of its own.

`#/design/board` is the inventory: every screen-state as a live-rendered frame on one pannable canvas,
laid out in menu order, with a screen's overlays in a rail beside it and its tabs below. Each frame
renders the real mock in a forced state — nothing inside a frame is clickable; *Open live* under a
frame loads that exact state interactively. The board only mounts frames near the view, and their
content mounts once the camera has settled, so panning stays cheap. The navigation is one toggle away
(lines, **off by default**): solid = a menu item or tab leads there, dashed = this control opens that
dialog. It is the only screen that renders outside `AppShell` — the shell is part of what each frame is
showing, so wrapping it would nest one panel inside another. Review frames from here and quote scene
ids; the route table is for when you want one screen full size.

`#/design/flow` is the journeys: `src/scenes/tasks.ts` declares six tasks as ordered scene ids, and
each renders as a row of numbered step cards — no canvas and no live frames. Every connector carries
the label of the thing that causes it: a registry link's own label for a declared link (so a rename
moves the diagram with it), or the menu control a nav hop goes through. A card links out to its frame
on the board and to the live mock at its exact state. Reading a flow happens here; looking at a frame
happens on the board.

`#/design/map` is the whole registry as cards — the board with its LOD floor above `maxZoom`, so no
live content can mount at any zoom — beside the reachability report `src/scenes/orphans.ts` derives:
overlays nothing triggers (empty, and asserted), everything else no link targets (entry screens and
forced states, listed with that reading stated), and the groups no menu item names. It is the surface
for "does every state have a way in?", in the only terms the registry can honestly answer.

### 4.10 Account (`/account`)

The single admin account, reached from the header's account dropdown. One `PageHeader` over three
cards: *Sign-in* shows the email read-only; *Password* has a default **Change password** button
opening a modal (current password, the new one twice, and a code when two-factor is on), with a
warning while the password is still the one in `RELIST_ADMIN_PASSWORD`, which the app shell also
flags in a banner; and *Two-factor authentication* carries an `On`/`Off`
tag, the action button, and a permanent note with the `reset-2fa` command for a lost authenticator.
Turning on is a three-step modal (Password → Scan → Verify): the QR code comes from the setup URI
with the secret copyable beside it for manual entry, and a complete six-digit code confirms it.
Turning off is one danger button opening a modal that asks for the password and a current code. Both
flows delete the account's other sessions, and both say so when they succeed.

### 4.11 Images (`/images`)

The host's Docker images and the one way to add to them: loading a `docker save` archive. One
`PageHeader` over an upload card and the list. The upload card is a single-file dragger that walks
five states: *idle* (the dragger), *uploading* (file name, percent and a Cancel button), *loading*
(all bytes sent, spinner and `Loading into Docker…`, no Cancel), *loaded* (a success `Alert` listing
each name, copyable) and *failed* (an error `Alert` with the server's message, or `Upload cancelled`).
The dragger is disabled while uploading or loading, and the browser warns before leaving the page. The
client refuses a wrong extension or a file over 10 GiB before sending. The list columns are Image
(every tag copyable, `<none>` when untagged), ID (12 hex characters), Size, Created (relative, absolute
in a tooltip) and In use; a search box filters by tag, and the rows of the last load are highlighted.
Uploads can run for up to one hour through Traefik; a longer one needs the direct-port fallback.

### 4.12 Deployments (`/deployments`)

Host-wide deployment activity, available below Projects in the side menu. Its badge
counts queued Deployments; the running Deployment is shown in the page's Queue.

- **Queue** remains unfiltered: running first, followed by all waiting Deployments
  in FIFO order, with their Project / Environment / Compose and current step.
- **History** queries all retained Deployments, newest queued time first, with
  Project, Environment, Compose, status and date filters and server pagination.
  Clearing a parent filter clears its dependent children and starts at page 1.
- Numbers belong to a Compose, so every row includes its context; `#1` alone does
  not identify a Deployment across the host.
- **View logs** opens the same right Drawer as the Compose tab. It follows the
  selected Deployment's steps, status and log, supports Cancel, and stops reading
  when closed or when leaving the page. Text stays within the available width.
- The page states the existing retention policy: the latest 20 Deployments per
  Compose. Deleted Composes and pruned records are not presented as available.
- Queue errors and History errors each offer Retry. Filters affect History only;
  no queue items disappear when an operator narrows the History.

### 4.13 Docker (`/docker`)

Host-level Docker reports and explicit cleanup. Purpose: inspect Docker storage and
remove eligible resources without opening a terminal.

- Keep Images at `/images`; Docker links to it with **Manage images**.
- Header offers **Refresh**; show Engine/API versions, host, storage driver/data directory,
  running/paused/stopped counts and the report's checked time.
- A storage table lists images, writable container layers, volumes, default-builder cache
  and networks. Sizes may overlap; reclaimable values are estimates. An unknown size or
  reference count is **Unavailable**, never zero. Volume estimates include named volumes.
- Inventories use Containers / Volumes / Networks tabs with pagination and table-local
  horizontal scrolling. Long names and paths wrap; they never expand the page.
- Cleanup buttons identify their scope: dangling images, stopped containers, unused custom
  networks, default-builder cache, anonymous volumes. **Advanced cleanup** contains all
  unused images (including tags) and all unused volumes (including names).
- Use `DangerModal` with `PRUNE`, or `DELETE VOLUMES` for volumes. State that cleanup affects
  the whole host, including resources outside Relist. Volume confirmations say deletion is
  permanent and a later failed deployment cannot restore data.
- Pruning is disabled while queue work, a mutation, unavailable queue/report data, a report
  refresh, or stale data is present. Backend independently excludes conflicting operations.
- Keep mutation errors and actual reclaimed bytes on screen. Refresh after either outcome;
  refresh failure keeps prior data labelled and disables further pruning. Never automatically
  retry a destructive request.
- Unknown prune completion pauses panel Docker work. Show **Resume Docker operations** with
  a typed `RESUME DOCKER` confirmation requiring the operator to verify daemon deletion has
  finished. This is an acknowledgement, not a cancellation or daemon completion probe.
- A *Server migration* card closes the page. It explains a server Migration and names the two
  commands as copyable code, `… relist-panel migration serve` on this server and
  `… relist-panel migration pull` on the new one. It calls no API and offers no token (ADR-0019).

### 4.14 Vulnerabilities (`/vulnerabilities`)

Known CVEs in the images this host runs: the panel's Composes, the panel itself, Traefik and
the Scanner (ADR-0021). The page answers two questions: "what should I rebuild?" and "how exposed
is the host?". It heads the side menu's **Security** group, above Audit log, and its badge is the host's Critical count.
The badge is hidden while scanning is off, because hidden is not 0.

- **Header:** **Refresh**, **Settings** (a right Drawer) and **Scan now**. Scan now is disabled
  with a tooltip while scanning is off.
- **Status bar:** when scanning is off, an info `Alert` reading `Scanning is off`, with
  **Open settings**. When it is on:
  - the queue: running, how many are waiting, or "waiting for deployments to finish";
  - the vulnerability DB date, tagged `Out of date` when it is older than 7 days or the last
    update failed;
  - the next daily run.

  A failed DB update is a warning `Alert` in the server's words. Scans carry on with the old DB.
- **Images tab:** one row per image in use.
  - **Columns:**
    - Image: refs and OS;
    - Used by: Services link to their Compose; Panel / Traefik / Scanner (Trivy) are plain text;
    - Critical / High / fixable tags;
    - Scanned;
    - a small **Scan** button and **Export report** (below).
  - **Export report:** a split button, disabled until the image has been scanned. Clicking copies a
    Markdown report for an AI agent to the clipboard (over plain HTTP, where there is no clipboard,
    it downloads instead and says so); its menu has **Download .md**. The report heads
    with the image ref and ID, then Fixable and No fix yet tables grouped by package, path and
    installed version, with the CVE IDs of each group. It states facts only, no instructions.
  - **Expanding a row** lists its findings: ID linked to the advisory, a `New` tag, severity,
    package and path, and `installed → fixed` or `(no fix yet)`.
  - **Failed scan:** the row shows `Last scan failed: <reason>` beside the last good counts,
    never instead of them.
- **CVEs tab:**
  - **Filters:** severity, Project, Compose, fixable only, new in 7 days, and a search. All of
    them live in the URL, so the badges on Overview, Project and Compose link straight into a
    filtered view. A filter that matches nothing says so.
  - **Table:** unique CVEs, server-paginated, with severity, title, fix, first seen and the
    images they affect.
- **Settings drawer:**
  - **Switches:** the master switch, plus after-deploy and daily, which are disabled while
    scanning is off. The daily time shows the panel's time zone. The master switch's help text
    gives the DB size and says that Java packages (jar files) are not scanned.
  - **Scanner image:** the field takes `repo[:tag]@sha256:…`. **Use default** fills in the
    built-in default. **Pick from recent tags** lists Docker Hub release tags from the last 14
    days plus the newest older one. Picking a tag younger than 14 days asks
    `This tag is less than 14 days old. Use it anyway?`.
  - **Save:** the button holds the spinner while a new image is pulled. A pull failure shows
    under the image field in Docker's words.
  - **Delete scanner cache:** a `Popconfirm` that names the size, enabled only while scanning
    is off. The cache can be downloaded again, so it gets no typed confirmation.
- **Badges elsewhere** (`VulnBadges`): `N Critical · N High · N fixable` tags.
  - **Where:** in the Overview card, the Project header and the Compose header. The Compose
    page also adds a column to its Services table.
  - **Links:** each links to this page with `?tab=cves&projectId=` or `&composeId=`.
  - **When off:** badges render nothing while scanning is off.
  - **Compose export:** the Compose header also carries **Export report** while scanning is on. Its
    report covers every image the Compose's Services run, and adds the Source (repo URL @ the commit
    of the last successful build, or the checked-out head before any build) and each Service's
    `build:` context and Dockerfile.

### 4.15 Panel update (`/panel-update`)

Updates the panel to the newest signed release without leaving the browser (ADR-0024). It sits
in the side menu's **Panel** group, below Users. The header shows the running version, and a
`processing` tag `Update available` that links here when a newer release was found; the header
re-reads the status every 10 minutes and shares the page's data, so a check on the page moves the
tag at once. No badge in the menu.

- **Header:** **Check now**, hidden while an update runs.
- **Page states.** One card at a time, chosen from the status and the preview:
  - **Up to date:** running version, last checked (`Never` if it has not), a **Daily check**
    switch, and "This panel is up to date." A server message (for example "This build has no
    release key", or a failed last check as a warning) is shown in place of that line.
  - **Update available:** an info `Alert` "Version X is available" with the release date, a
    **Release notes** link and a primary **Preview** button. Nothing is written by Preview.
  - **Preview:** a card with the versions, release notes, whether the Install file is
    `Not edited`, `Edited, merged` or `Edited, conflict`, and tags for what is true right now
    (Install found, Migration, Deployments running). Then, as they apply:
    - the conflict, both sides in a `DiffViewer` plus the merge with its markers in a closed
      `details`;
    - the merged diff for `Edited, merged`;
    - **Install checks**, one line per Install invariant with a success or error icon (never
      colour alone); a failing one says why and which file set it;
    - the Services that will be recreated, with a warning `Alert` when Traefik is one of them;
    - **Before you update**, one `Checkbox` per manual step.
  - **Cannot start:** the same card titled "The update cannot start", with an error `Alert` for
    a failed preview step or a failing invariant, and no **Update** button.
  - **Manual:** a warning `Alert` "This Install needs a manual update" with the reason and a link
    to the README, for an Install the Updater cannot drive or a release with checks this panel does
    not know.
  - **Waiting:** a card for a run in `quiescing`: what the run waits on (`Deployment api #3`, or
    a vulnerability scan), the elapsed time, and a **Cancel** button (`danger`) that stops the
    Updater; nothing has been written yet. The header hides **Check now**.
  - **Updating:** a vertical `Steps` of the run's steps, plus "Roll back" as a final step while it
    rolls back. A run that went on without the panel answering shows its note as a warning
    `Alert`. No buttons.
  - **Restarting:** a card with the elapsed time. After 5 minutes without an answer it becomes
    "The panel is not answering" with the shell commands to run on the host (copyable).
  - **Result of the last run** (above the card, closable): `Update cancelled` (info, "Nothing was
    written"; a run the panel refused under a Migration lock shows that reason as the title);
    `Rolled back to X` (warning, with the
    reason); `The rollback failed` (error, with both errors, the backup path and the restore
    commands); `The update was interrupted` (warning, with the backup path and the check
    commands). After a success the page reloads once onto the new bundle and shows a closable
    success `Alert` "Updated to X".
- **Confirmation:** **Update** is disabled until every manual step is ticked and the preview is
  ready. It opens a `Modal.confirm` "Update to X?" that states the restart, the self-rollback,
  the Services recreated (and Traefik's few seconds of dropped Domains), and that queued
  Deployments are cancelled, a running Deployment or scan is waited for, and new ones are refused
  until the update ends. A 409 (the files changed
  since the preview) closes the preview with an error: run it again.
- **Preview:** the preview is the report of the `update --preflight` Container; the page shows the
  live gate (Install found, Migration, Deployments running) next to it. A preview that finds the
  panel up to date shows an info notice instead of a card.
- **Polling:** the page reads the status every 2 seconds while a run is in flight, including one
  started elsewhere, until it ends; there is no polling otherwise. A failed read during the run
  is "Restarting", not an error; a 401 sends you to sign in (sessions can be lost on a rollback).

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
- The sider stays at viewport height; navigation scrolls independently so its
  Docker host footer remains visible. The footer warns at **80% or more** used on
  the filesystem containing Panel/SQLite data, shows percentage and free space,
  and links to Monitoring. It refreshes every 10 seconds while signed in. Missing
  readings are unavailable; readings older than 30 seconds or from a failed
  refresh are labelled stale, with the last reading identified explicitly.
  Under it, a secondary `Relist Panel <version>` line (`dev` for an unstamped
  build) links to Panel update; it is hidden until the status has loaded.
  Below the `lg` breakpoint navigation opens as an overlay, preserving the page
  width; its trigger, backdrop or a navigation selection can close it.
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
  with the editor sized to its content, between `LAYOUT.editorAutoMinHeight` and
  `LAYOUT.editorAutoMaxHeight`.
- No nested scrollers. Pages scroll with the document; the only bounded boxes are the editor, the logs
  viewer and the terminal, where a bounded box *is* the feature. The terminal's height is
  `LAYOUT.terminalHeight` (`clamp(420px, 65vh, 800px)`), because a shell needs a viewport but the page
  must still scroll as one. Inside the full screen editor the modal itself
  is the bounded box: it states its own height (`100vh`) and nothing in it scrolls the page — the one
  place that scrolls internally is the fixed 340px variables column beside the file, which has no
  height of its own to grow into.
- Tables over card grids past ~12 items; the Projects screen ships both and lets the operator choose.
- The Volume Explorer follows the same rule with a shorter box: its editor is
  `LAYOUT.volumeEditorHeight` (`clamp(320px, 45vh, 560px)`), because a screen whose purpose is
  browsing a directory cannot give the editor the viewport. The file list runs long and the page
  scrolls as one document, exactly like every other screen.
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
| `ComposeTerminalTab` | `components/ComposeTerminalTab.tsx` | The Compose screen's Terminal tab body: Service/Container Select, Shell and User Segmented, Connect, and the xterm box at LAYOUT.terminalHeight. One session per page; output survives a lost connection. |
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
  removing containers, volumes or files. A file delete inside a Volume is a `Popconfirm` naming the
  path — permanent, with no trash, because a hidden trash directory inside the Volume would be
  visible to the application using it.
- **Warn; do not gate.** Writing to a Volume whose Compose is running can corrupt a live database,
  and that write is deliberately **not** behind an acknowledgement. Since the banner is then the only
  control, it has to name the consequence and the affected Services — never "be careful" — and it
  lets the write proceed. The one acknowledgement the Explorer asks for is an unverifiable bind
  source — an absolute path outside the data directory that the panel cannot `stat`, where the mount
  itself is what would create it. It asks once per visit, before the first operation. A gate is
  reserved for what cannot be noticed afterwards.
- **Nothing expires on its own.** Every operation ends with its own `docker run --rm`, so there is no
  idle auto-stop and the Explorer never empties itself while the operator is elsewhere.
- **Errors carry their source's words.** A helper refusal is shown as the helper worded it —
  including a permission failure naming the uid the operation runs as — not paraphrased into something
  generic. An operator can act on a uid; they cannot act on "something went wrong".
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
| A Compose's Volume list | Nothing | `docker compose -p <project> -f <file> --env-file <env> config --format json`, parsed for `services.*.volumes[]` (type, source, target, read_only) plus the top-level `volumes` map for real names. Verified to resolve relative binds, `${VAR}` interpolation and each service's `user:` in the one call — 30 ms, so no caching needed. |

## 12. Implementing this in `web/`

*Landed — `.scratch/design-port/` ran this order to close; kept as the record of how the port ran.*

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
