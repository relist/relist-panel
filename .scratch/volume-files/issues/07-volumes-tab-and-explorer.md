# 07 — Volumes tab and Volume Explorer — the read-only slice

**What to build:** The tracer bullet. After this ticket an operator can open a Compose, see the
Volumes it mounts, open the Explorer on one, browse it, and download a file or a directory — end to
end. Every integration risk in the design lives at or before this point; everything after it is
ordinary work.

**Blocked by:** 05

**Status:** ready-for-human

- [x] The Compose page gains a third tab, Volumes, listing only that Compose's Volumes with kind, resolved source, target, and the container-side path each Service uses
- [x] Before the first deploy the tab states why it is unavailable, carrying the CLI's `couldn't find env file`
- [x] The Volume Explorer is its own route in the panel SPA, opened in a new browser tab, behind the panel's existing session auth — not a separate web app, and with no nav entry of its own since a Volume is meaningless without its Compose
- [x] It lists a directory with name, size, mtime, owner uid/gid, mode and kind; navigates into directories; shows symlinks with their targets and refuses the ones that escape with a clear message
- [x] A toolbar covers the read actions: refresh, download a file, download a directory as tar.gz. The slice is read-only — no write controls, no Monaco
- [x] Resolved paths are shown (the host path for a bind source, `/var/lib/docker/volumes/<real>/_data` for a named Volume)
- [x] A bind source outside the data directory gets a confirmation before its first operation, even though the operator declared it
- [x] Document flow throughout, no nested scrollers (`docs/GUIDELINES.md` §6) — the file list runs long on the page
- [x] The feature is unavailable, tab hidden, when the panel is not running in a container
- [x] UI work happens directly in `web/`, per ADR-0009, with the annotated tag `design-mock` as the visual reference for the screens and states

## Comments

**session 1/2:** the Volumes tab landed. `web/src/data/` gains the `ComposeVolumes` wire types,
`api.listVolumes` and `volumeArchiveUrl` (the direct `?source=…&path=.` download handed to an
`<a href>`, so the backup streams to disk and never through a fetch/blob); the store gains
`loadVolumes` under the `volumes:<composeId>` pending key. The page owns the one fetch the tab's
existence depends on: `available: false` (bare binary / no Docker socket) hides the tab entirely,
and a URL that still names it redirects to the Editor with every other stale link — so the tab
never fetches twice and never renders dead; a deploy refetches, because the env file it writes is
what the resolution needs. `web/src/components/ComposeVolumesTab.tsx` renders the states the pure
`web/src/components/volumes-view.ts` derives: the loading skeleton, the not-deployed `Result` with
the CLI's own `couldn't find env file` and a Deploy affordance, any other failure as an Alert with
Retry, the empty state, and the populated table — kind tag, logical name or resolved source, the
resolved path (bind source, or `/var/lib/docker/volumes/<real>/_data`), every Service's
container-side target with its `ro` marker, pre-flight `missing`/`unverifiable` warnings as Alerts
naming the consequence, and a per-Volume `Download tar.gz` disabled with the API's own reason when
the source is missing. Document flow only — a plain Table, no nested scrollers. The Explorer route,
its toolbar and the bind confirmation stay for session 2.

Evidence: `pnpm typecheck && pnpm test` under `node:24-alpine` — tsc clean, 151/151 green (the 15
new `volumes-view` tests: loading, not-deployed message, unavailable, error, empty, populated
warnings, resolved path, label, refusal; plus `activeComposeTab('volumes')`); `pnpm build`
completes as a sanity check.

**session 2/2:** the Explorer landed. `paths.volumeExplorer(projectId, environmentId, composeId, source, path)`
builds `…/compose/:id/volumes/explorer?source=…&path=…` — the resolved source and the relative path ride in the
query because either can hold slashes, spaces or newlines, each parameter percent-encoded whole, so the new tab
survives a reload; the router gains one static leaf that out-ranks the Compose `:tab` route, and `…/volumes`
stays the tab. The route sits inside `RequireAuth` with no sider entry, and the Volumes tab's rows gain `Open`
(`target="_blank"`, `rel="noreferrer"`) — disabled with the API's own reason where the pre-flight marks the source
missing. `web/src/pages/VolumeExplorerPage.tsx` resolves the Compose and the Volume from the URL, states its
identity (label, resolved host path, every Service target with its `ro` marker) and derives its states from the
pure `web/src/pages/volume-explorer-view.ts`: loading skeleton, empty directory, read error with Retry, refused
symlink, and the missing-source refusal carrying the API's 409 wording. The file list runs long — a plain Table,
no pagination, no scrollers — with name, size, mtime, owner uid/gid, mode and kind columns. Directories navigate on
click; a symlink is probed by listing it: the root resolves it (the screen follows it) or answers
`not a directory` (it is selected and downloads through `read`, which follows inside-root links) or refuses it —
`path escapes from parent` surfaces in an alert with the target disclosed, and a target that must leave the root is
also tagged `outside the Volume` in the row. The toolbar is read-only: refresh, `Download file`
(`read?download=1`) and `Download tar.gz` (`archive`), both direct anchors, never fetch+blob, and both disabled
with the API's reason on a missing source. An `unverifiable` bind (outside the data directory) gates every
list/read behind one dialog per visit stating that continuing mounts the path and Docker creates it `root:root
0755` if missing; declining pauses reads rather than dismissing the screen. `api.ts` gains `listVolumeEntries` and
`volumeReadUrl`, and `volumeArchiveUrl` now takes the directory to archive — the tab's whole-tree call is
unchanged. No write controls, no Monaco, no restore.

Evidence: `pnpm typecheck && pnpm test` under `node:24-alpine` (frozen lockfile, warm pnpm store) — tsc clean,
182/182 green (31 new: 25 `volume-explorer-view` — the resolution states, the missing-source 409 wording, the
unverifiable gate, breadcrumb path math, entry formatting, symlink escape/`not a directory` mapping and the
open-failure copy — plus 3 `paths` query round-trip/route-resolution tests and 3 direct-download URL tests);
`pnpm build` completes as a sanity check (the rebuilt `internal/webassets/dist` was restored, not committed).
