# Design states — every state the design mock defined

> **Provenance.** Generated from the scene registry of the retired design mock (`web-design/`,
> tag `design-mock`) on 2026-09-27, the day the mock left the working tree. The mock was the
> design contract while `web/` was ported (`.scratch/design-port/`, closed); this file is the part
> that outlives it — the checklist of every state the design defined, the route each one lives at,
> and what forces it. The rules themselves live in [`GUIDELINES.md`](./GUIDELINES.md).

**How to read a row:**

- `` `scene-id` `` — the id quoted in review feedback and in the mock's URLs (`?scene=<id>`).
- *Title* — what the frame was called on the board.
- `#/route` — where the state lives in the real app (`web/` uses the same paths as history URLs).
- *forced by* — the state knobs: `ui:<key>=<value>` is one of the app's addressable `?ui=` keys,
  `after:<adapter>` is the adapter that fires after mount (a toast, an error). A state with neither
  is just its route.
- *(overlay)* / *(feedback)* — a dialog or a feedback state rather than a screen.

Total: **102 states in 9 groups.**

The mock's own code, including the registry this was rendered from, is one `git worktree add /tmp/design-mock design-mock` away.

---

## Overview — 7 states

- `overview` — Overview · `#/`
  The landing read: the attention alert names the Composes that are not fully up (worker is partial, registry is stopped), and beneath it the live-handle banner, activity and every Compose — check that the page answers "is anything wrong?" before you scroll.
- `overview-all-clear` — Overview — all clear · `#/` · forced by after:seed:overview.allClear
  Attention is derived, not stored: with every Compose fully running the alert disappears and the Needs-attention card shows the empty state, so check that "All clear" reads as information and not as a broken page.
- `overview-refreshing` — Overview — refreshing · `#/` · forced by ui:overview.refreshing=1
  Refresh statuses re-checks every Compose on the host; the spinner holds the button without blanking the page, which is the difference between "busy" and "lost".
- `overview-handle-orphaned` — Overview — orphaned Volume Handle · `#/` · forced by ui:store.handles="9:/srv/retired-data"
  A Handle whose Compose is gone can still be open, so the banner names it "a deleted Compose" instead of a dead link — check that the offer is Close, not Browse.
- `shell-account-menu` — Shell — account menu · `#/` · *(overlay)* · forced by ui:shell.accountMenu=1
  The header is on every screen, so its one dropdown is reviewed once: check that sign-out is separated from navigation, because it is the only control here that ends the session.
- `shell-command-palette` — Shell — command palette · `#/` · *(overlay)* · forced by ui:shell.paletteOpen=1
  Keyboard-first navigation over the mock's real routes: check that the hits are Composes and screens you would actually jump to, not a second menu.
- `shell-command-palette-empty` — Shell — command palette, no match · `#/` · *(overlay)* · forced by ui:shell.paletteOpen=1, ui:palette.query="deploy"
  Typing a query nothing matches is the palette's only dead end; the empty copy names the query back so it reads as "no match for deploy", not "broken".

## Projects — 9 states

- `projects-cards` — Projects — cards · `#/projects`
  The default view: one card per Project with its Compose rollup, so a degraded app is visible before you open anything — check the partial worker shows on my-saas without drilling in.
- `projects-table` — Projects — table · `#/projects` · forced by ui:projects.view="table"
  Same data, operator density: sortable columns and per-row actions for someone running many Projects — check that the rollup survives the denser layout.
- `projects-filtered-empty` — Projects — filter matches nothing · `#/projects` · forced by ui:projects.query="billing"
  A filter that matches nothing must say so against the query it was given — the empty card quotes "billing" back, so a typo reads as a typo and not as lost Projects.
- `projects-empty` — Projects — nothing yet · `#/projects` · forced by after:projects:empty
  First-run has no Projects to roll up: the empty state sells the next action (Create a Project) instead of an empty grid — the one Projects state the store cannot seed today, reached through an adapter (see ticket Comments).
- `projects-row-menu` — Projects — row menu · `#/projects` · *(overlay)* · forced by ui:projects.view="table", ui:projects.rowMenu.landing=1
  Per-row state is addressed by naming the row, or one boolean would open every menu at once — check that Edit and Delete sit behind one click and not inline.
- `projects-new-modal` — Projects — New Project · `#/projects` · *(overlay)* · forced by ui:projects.modalOpen=1
  Creation is two fields on purpose: check that the modal promises no Environment or Compose decisions, because those belong to the Project screen after it exists.
- `projects-new-modal-errors` — Projects — New Project, invalid · `#/projects` · *(overlay)* · forced by ui:projects.modalOpen=1, after:submit:projects.new.empty
  Validation only exists after a submit, so the frame is the real handler's result: errors under the name field, the modal still open, nothing the user typed lost.
- `projects-edit-modal` — Projects — Edit Project · `#/projects` · *(overlay)* · forced by after:object:projects.editing
  The same modal as New, prefilled through the app's own setter with a real Project: check the title says Edit and that name and description arrive as stored.
- `projects-delete-modal` — Projects — Delete Project · `#/projects` · *(overlay)* · forced by after:object:projects.deleting
  The typed-name confirm for landing: OK stays disabled until the name matches, because deleting a Project takes its Environments and Composes with it and retyping the name is the cost of meaning it.

## Monitoring — 4 states

- `monitoring` — Monitoring · `#/monitoring`
  One host, six series, at the default 1h window: memory rides its sawtooth into the warn tone, so check the chart's colour and the headline agree about how close to the ceiling this is.
- `monitoring-loading` — Monitoring — loading · `#/monitoring` · forced by after:monitoring:loading
  Six skeletons, not one: loading is a data state, so it is held by an adapter rather than a forced boolean — check the skeleton keeps each chart's slot so the page does not jump when data lands.
- `monitoring-6h` — Monitoring — 6h window · `#/monitoring` · forced by ui:monitoring.window="6h"
  Beyond the hour the panel rolls up to one-minute samples: check the 6h window still shows the deploy spike and the backup bump instead of flattening them away.
- `monitoring-24h` — Monitoring — 24h window, partially collected · `#/monitoring` · forced by ui:monitoring.window="24h"
  The panel has been up 187 minutes, so a 24h window has 187 of 1440 minutes: check the "collecting" note says what is missing instead of drawing the line as if the host were idle all day.

## Project — 21 states

- `project-composes` — Project — Composes · `#/projects/1`
  my-saas / production: the Environment switcher keeps the context while the table lists web, worker and metrics — check the mixed states (running, partial, never deployed) read at a glance in one row.
- `project-composes-empty` — Project — Composes, empty Environment · `#/projects/1/env/3/composes`
  preview has no Composes, so the empty state offers "Write the first Compose" — the copy names the Environment it is about, because an empty tab must not read as a load failure.
- `project-composes-row-menu` — Project — Composes row menu · `#/projects/1` · *(overlay)* · forced by ui:project.rowMenu.web=1
  Deploy, Restart, Stop and Delete per Compose row, addressed by naming the row: check that the destructive entry is separated from the lifecycle ones.
- `project-404` — Project — not found · `#/projects/99`
  A stale link to a deleted Project must dead-end politely: the 404 names what is missing and offers the way back to Projects instead of an empty shell.
- `project-zero-envs` — Project — no Environments · `#/projects/4` · forced by after:seed:project.zeroEnvs
  A Project can exist before its first Environment: the switcher is replaced by a single call to create one — no fixture Project has zero Environments, so the state is adapter-backed (see ticket Comments).
- `project-new-compose-empty` — Project — New Compose, empty YAML · `#/projects/1` · *(overlay)* · forced by ui:project.composeModalOpen=1
  OK is disabled until there is YAML: a Compose is created from a real file, never from a name alone — check the disabled state explains what is missing.
- `project-new-compose-filled` — Project — New Compose, YAML pasted · `#/projects/1` · *(overlay)* · forced by ui:project.composeModalOpen=1, after:fixture:project.composeYAML
  The starter YAML arrives through the app's own setter, so OK enables: check the editor is where the deploy actually begins, not a name field with a file upload bolted on.
- `project-new-env-modal` — Project — New Environment · `#/projects/1` · *(overlay)* · forced by ui:project.environmentModalOpen=1
  One field, because an Environment is a scope and not a deployment: check the modal does not imply it will deploy anything.
- `project-delete-compose-modal` — Project — Delete Compose · `#/projects/1` · *(overlay)* · forced by after:object:project.deletingCompose
  The typed-name confirm seeded with web through the app's own setter: the consequence line says what happens to the containers, so check the copy and the disabled OK agree that this is deliberate.
- `project-delete-env-popconfirm` — Project — Delete Environment · `#/projects/1/env/1/settings` · *(overlay)* · forced by ui:project.deleteEnv.staging=1
  The Popconfirm carries the arithmetic ("this stops and removes every Compose in it"), addressed per row: a lightweight confirm is honest only while the consequence fits in one sentence.
- `project-delete-modal` — Project — Delete Project · `#/projects/1/env/1/settings` · *(overlay)* · forced by ui:project.deleteProjectOpen=1, ui:danger.typed="my-saas"
  Danger zone, name typed: OK has just enabled because the input matches — the frame exists to show that moment, where the cost of the action is retyping what you are about to lose.
- `project-variables` — Project — Variables · `#/projects/1/env/1/variables`
  production's six keys, secrets masked by default: check the reveal control is per value and the type tag (secret/plain) is visible without exposing anything.
- `project-variables-revealed` — Project — Variables, one revealed · `#/projects/1/env/1/variables` · forced by ui:variables.revealed.DATABASE_URL=1
  Reveal is addressed per key, so exactly one value is exposed while its neighbours stay masked — check that the revealed state is visibly deliberate (icon state flipped), not a rendering bug.
- `project-variables-filtered-empty` — Project — Variables, filter matches nothing · `#/projects/1/env/1/variables` · forced by ui:variables.query="GRAFANA"
  The search narrows to nothing: the empty row names the query back, so a zero reads as "no match for GRAFANA" and not as lost variables.
- `project-variables-empty` — Project — Variables, empty Environment · `#/projects/1/env/3/variables`
  preview has no keys yet: the empty state points at Paste a .env, because the honest way to fill this table is the file the operator already has.
- `project-variable-new-modal` — Project — New variable · `#/projects/1/env/1/variables` · *(overlay)* · forced by ui:variables.modalOpen=1
  Key, value and the secret switch on one modal: check the key rules (letters, digits, underscores) are stated where they will be violated.
- `project-variable-edit-modal` — Project — Edit variable · `#/projects/1/env/1/variables` · *(overlay)* · forced by after:object:variables.editing
  The same modal prefilled with DATABASE_URL through the app's own setter: check the key is editable and the value arrives masked exactly as the row showed it.
- `project-paste-env` — Project — Paste a .env, parsed · `#/projects/1/env/1/variables` · *(overlay)* · forced by ui:variables.bulkOpen=1, after:fixture:variables.bulkText
  The paste is the state: the fixture .env is parsed into a preview table before anything is committed, so check the "Replace N variables with M" statement is the OK button's own label.
- `project-paste-env-duplicates` — Project — Paste a .env, duplicate key · `#/projects/1/env/1/variables` · *(overlay)* · forced by ui:variables.bulkOpen=1, after:fixture:variables.bulkDuplicate
  A duplicate key refuses the whole paste, naming the offending key and disabling OK: a bulk replace that silently picked a winner would be the worst kind of surprise.
- `project-delete-variable-popconfirm` — Project — Delete variable · `#/projects/1/env/1/variables` · *(overlay)* · forced by ui:variables.deletePopconfirm.DATABASE_URL=1
  Deleting one key costs one Popconfirm naming the key, not a typed-name modal: check the escalation is proportional — this is reversible by pasting the value back.
- `project-settings` — Project — Settings · `#/projects/1/env/1/settings`
  Project details, the Environments table and the danger zone on one tab: check that Delete Project is visually the last thing on the page and the only red one.

## Compose — 21 states

- `compose-editor-running` — Compose — Editor, running · `#/projects/1/env/1/compose/1`
  web is fully running and the YAML is in sync: check the status tag, the deploy identity (immutable per ADR-0005) and the editor share one page without the YAML feeling like a form.
- `compose-editor-partial` — Compose — Editor, partial · `#/projects/1/env/1/compose/2`
  worker is 1 of 2: the exited worker-cron sits in the service list with its state, so check the page says which half is down without opening Monitor.
- `compose-editor-new` — Compose — Editor, never deployed · `#/projects/1/env/1/compose/3`
  metrics has YAML but no deploy: the primary action is Deploy, and nothing implies containers exist — check Monitor and Logs degrade honestly for this state (their own scenes cover it).
- `compose-editor-dirty` — Compose — Editor, unsaved changes · `#/projects/1/env/1/compose/1` · forced by after:fixture:compose.draft
  The draft is seeded through the app's own setter, so the dirty banner appears with Discard and Save: a deploy does not pick up the edit until Save, and the banner is where the page says so.
- `compose-editor-404` — Compose — not found · `#/projects/1/env/1/compose/999`
  A stale deep link (a deleted Compose, an old tab): the 404 offers the Environment it used to live in, because that is where the replacement would be.
- `compose-more-menu` — Compose — More menu · `#/projects/1/env/1/compose/1` · *(overlay)* · forced by ui:compose.moreOpen=1
  Restart, Stop and Delete live behind one menu so the header keeps a single primary action: check Deploy is the only thing that stays out in the open.
- `compose-delete-modal` — Compose — Delete Compose · `#/projects/1/env/1/compose/1` · *(overlay)* · forced by ui:compose.deleteOpen=1, ui:danger.typed="web"
  Typed-name confirm at the moment OK enables: the modal names what stops and what is removed, and the frame is here because that instant — enabled but not yet clicked — is the decision.
- `compose-monitor` — Compose — Monitor · `#/projects/1/env/1/compose/1/monitor`
  Per-service charts with the deploy marker on the axis: check picking a service in the Select re-charts without losing the window, and that the marker sits on a burst worth correlating.
- `compose-monitor-empty` — Compose — Monitor, nothing to monitor · `#/projects/1/env/1/compose/3/monitor`
  metrics was never deployed, so there are no containers and no samples: the empty state says why (nothing deployed) instead of drawing an empty chart.
- `compose-monitor-loading` — Compose — Monitor, loading · `#/projects/1/env/1/compose/1/monitor` · forced by after:monitor:loading
  Loading is a data state held by an adapter: check the skeletons land per chart with the Select and window already in place, so the toolbar never flashes.
- `compose-monitor-partial` — Compose — Monitor, exited service · `#/projects/1/env/1/compose/2/monitor`
  worker-cron exited, so its line stops mid-window and keeps the history it had: check the chart does not flatline to zero, which would read as "quiet" instead of "dead".
- `compose-volumes-live` — Compose — Volumes, resolvable · `#/projects/1/env/1/compose/1/volumes`
  web resolves to all six Volume shapes — named, bind, read-only, unverifiable and the absent ./retired whose Open is disabled: check every row states where it is on the host and who mounts it.
- `compose-volumes-unresolvable` — Compose — Volumes, not resolvable · `#/projects/1/env/1/compose/3/volumes`
  Resolution runs docker compose config, which fails until a first deploy writes the env file: the card quotes the CLI error and offers Deploy as the way out, so the dead end explains its own key.
- `compose-volumes-none` — Compose — Volumes, nothing mounted · `#/projects/1/env/1/compose/4/volumes`
  staging web declares no volumes: the empty state says "nothing mounted" as a fact about the Compose file, not as a resolution failure — the two empties must not look alike.
- `compose-volumes-loading` — Compose — Volumes, resolving · `#/projects/1/env/1/compose/1/volumes` · forced by after:volumes:loading
  Resolution is a data fetch held by an adapter: check the skeleton occupies the table's rows so the tab does not reflow when the list lands.
- `compose-logs` — Compose — Logs drawer · `#/projects/1/env/1/compose/1` · *(overlay)* · forced by ui:compose.logsOpen=1
  The drawer beside the editor, service filter and line count above a stream: check the newest line is what auto-scroll is showing, and that Copy takes the visible lines only.
- `compose-logs-fatal` — Compose — Logs, fatal exit · `#/projects/1/env/1/compose/2` · *(overlay)* · forced by ui:compose.logsOpen=1
  worker-cron's last lines are the whole story — fatal ENOENT, exit code 1 — so check the error is findable in a stream that also contains the healthy worker's noise.
- `compose-logs-loading` — Compose — Logs, loading · `#/projects/1/env/1/compose/1` · *(overlay)* · forced by ui:compose.logsOpen=1, ui:compose.logsLoading=1
  The drawer opens before the fetch lands: check the skeleton keeps the drawer's own shape (filters included) so opening never looks like an empty log.
- `compose-logs-empty` — Compose — Logs, empty · `#/projects/1/env/1/compose/3` · *(overlay)* · forced by ui:compose.logsOpen=1
  metrics has never run, so there is no log: the drawer must say "no output" as a state, because an empty pre block reads as a broken stream.
- `compose-fullscreen` — Compose — Fullscreen editor · `#/projects/1/env/1/compose/1` · *(overlay)* · forced by ui:compose.fullscreenOpen=1
  The editor at full viewport with the variables rail beside it: check a ${VAR} reference in the YAML is copyable from the rail, which is the reason the rail exists.
- `compose-fullscreen-empty-rail` — Compose — Fullscreen editor, rail filtered to nothing · `#/projects/1/env/1/compose/1` · *(overlay)* · forced by ui:compose.fullscreenOpen=1, ui:fullscreen.railQuery="GRAFANA"
  A rail query nothing matches narrows to an empty list while the edit continues underneath: the YAML must stay interactive, because the rail is a reference and not a gate.

## Volume Explorer — 24 states

- `volume-explorer-root` — Volume Explorer — root listing · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads`
  The uploads root in one frame: the running-stack write warning (rw handle, api up), the escaping legacy symlink, the 4.2 MB export.csv and the filename with a newline — check the screen is honest about all four without a click.
- `volume-explorer-subdir` — Volume Explorer — subdirectory · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads/invoices/2026-09`
  Three levels down via the breadcrumb: check the crumbs stay navigable (every segment clickable) and the listing is still one Volume, not a new context.
- `volume-explorer-no-handle` — Volume Explorer — no handle open · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads` · forced by ui:store.handles="none"
  A Handle exists only while someone has it open, so with none the screen shows an explanation and one button — check the copy explains the model instead of implying an error.
- `volume-explorer-expired` — Volume Explorer — handle closed by idle · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads` · forced by ui:store.handles="none", ui:volume.expiredHere=1
  The handle stopped itself after 30 idle minutes: the alert says nothing was changed and reopening costs one click, because an auto-stop must never read as data loss.
- `volume-explorer-unreadable` — Volume Explorer — unreadable directory · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads/avatars`
  avatars is 0700 root:root and the handle runs as uid 999: the error quotes the helper message with the uid in it, because the uid is the thing an operator can actually change.
- `volume-explorer-404` — Volume Explorer — Volume not found · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fnope`
  A Volume removed from the Compose since the link was made: the 404 says so and offers the way back to the Volumes tab, which is where the current list lives.
- `volume-explorer-compose-unresolvable` — Volume Explorer — Compose not resolvable · `#/projects/1/env/1/compose/3/volumes/3%3A%2Fvar%2Flib%2Fgrafana`
  metrics cannot resolve its volumes at all, so the explorer cannot open a handle: check the screen refuses upstream of the dialog instead of pretending the Volume exists.
- `volume-explorer-loading` — Volume Explorer — loading · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads` · forced by after:volume:loading
  Listing a directory is a fetch, held by an adapter: check the skeleton keeps the toolbar and breadcrumb so the screen does not jump when the rows arrive.
- `volume-explorer-readonly` — Volume Explorer — read-only handle · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fconfig` · forced by after:seed:volume.handleRo
  config is a read-only bind opened ro: the banner says editing, uploading and deleting are off, and the row actions are gone rather than disabled-invisible — the seeded handle is rw, so this frame is adapter-backed (ticket Comments).
- `volume-explorer-editor-open` — Volume Explorer — file editor open · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads` · forced by after:object:volume.editing
  README.txt open in the editor card below the listing, seeded through the app's own setter: check Save writes back to the Volume and Cancel leaves without a toast.
- `volume-explorer-too-large` — Volume Explorer — too large to edit · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads` · forced by after:object:volume.editorTooLarge
  export.csv is 4.2 MB past the 1 MB cap: the refusal names the size, the cap and the alternative (download), because the honest move is to refuse Monaco rather than hang it.
- `volume-explorer-upload-size` — Volume Explorer — upload refused, too large · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads` · forced by after:seed:volume.uploadTooLarge
  An oversized upload is refused with the file's name and the limit in the alert, reached through the app's own refusal path so the copy is the app's: check nothing half-uploaded remains.
- `volume-explorer-upload-space` — Volume Explorer — upload refused, no space · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads` · forced by after:seed:volume.uploadNoSpace
  The other refusal: the Volume is out of space, so the alert names the volume rather than the file — the two upload failures must be tellable apart.
- `volume-explorer-empty-dir` — Volume Explorer — empty directory · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads/incoming`
  incoming is emptied by the cleanup job: an empty table is a true fact about the Volume here, so check it reads as "no files" and not as a failed listing.
- `volume-explorer-new-file` — Volume Explorer — New file · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads` · *(overlay)* · forced by ui:volume.newEntry="file"
  The modal states where the file lands and who will own it (the handle's uid) before it exists: check OK stays disabled while the name is empty.
- `volume-explorer-new-folder` — Volume Explorer — New folder · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads` · *(overlay)* · forced by ui:volume.newEntry="dir"
  Same modal, different title and placeholder: one component serving both, so check nothing about the copy still says "file" in the folder state.
- `volume-explorer-download-dialog` — Volume Explorer — Download as archive · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads` · *(overlay)* · forced by ui:volume.downloadOpen=1
  The disclosure is the point: this tree contains an escaping symlink, and the dialog counts it before you extract — check the warning is above the OK button, not below the fold.
- `volume-explorer-row-menu` — Volume Explorer — row menu · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads` · *(overlay)* · forced by ui:volume.rowMenu.latest=1
  Download, Rename and Delete per row, addressed by naming the row: check Rename says out loud that it is out of scope for the design pass instead of failing silently.
- `volume-explorer-delete-popconfirm` — Volume Explorer — delete entry · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads` · *(overlay)* · forced by ui:volume.deletePopconfirm.legacy=1
  "Not a trash — there is no undo", naming the entry and, for a directory, everything under it: the audit also found this Popconfirm has no onConfirm yet (ticket 02 §5.6), so the confirm is currently a no-op — recorded, not fixed here.
- `start-handle-rw` — Start Handle — read-write, stack running · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads` · *(overlay)* · forced by ui:volume.startOpen=1
  The default (rw, uid 999 from the container) with api and redis up, so the write warning names them: this is the banner an operator most needs to read before typing, because there is no confirmation gate after it.
- `start-handle-ro` — Start Handle — read-only · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fconfig` · *(overlay)* · forced by ui:volume.startOpen=1, ui:startHandle.mode="ro"
  Switching to ro on the read-only bind: the dialog says what ro costs (no edits) and nothing about corruption, because a read-only mount cannot corrupt a running stack.
- `start-handle-unverifiable` — Start Handle — unverifiable path · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fsrv%2Flegacy-images` · *(overlay)* · forced by ui:volume.startOpen=1
  An absolute bind outside the data directory cannot be stat-ed without mounting it: the warning says so and names the root:root 0755 an accidental mount would create — honesty instead of a guess.
- `start-handle-absent` — Start Handle — absent path refused · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fretired` · *(overlay)* · forced by ui:volume.startOpen=1
  ./retired is declared but was never provisioned, so OK is disabled: mounting it would create it, and the refusal says what would — this is the state ticket 04's fixture addition made reachable.
- `start-handle-root` — Start Handle — uid typed by hand · `#/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads` · *(overlay)* · forced by ui:volume.startOpen=1, ui:startHandle.uid="0", ui:startHandle.gid="0"
  Typing 0:0 flips the source tag to "set by hand" and raises the root warning: the frame exists because a manual uid is sometimes the only fix and always the dangerous one.

## Design system — 1 states

- `guidelines` — Guidelines · `#/design/guidelines`
  The panel documenting itself — spacing, type, tables, feedback rules and the live examples under each: one tall frame on purpose, so a rule and its example are reviewed in the same glance.

## Auth — 4 states

- `auth-signin` — Sign in · `#/signin`
  The split screen exists so sign-out does not feel like an error page: check the form is the focus and the left panel says what this panel is before asking for credentials.
- `auth-signin-submitting` — Sign in — submitting · `#/signin` · forced by ui:auth.submitting=1
  The button holds its own loading state for the length of the mock's 500 ms sign-in: check nothing else on the form disables, because backspacing the email mid-flight should stay possible.
- `auth-signin-errors` — Sign in — validation errors · `#/signin` · forced by after:submit:auth.signin.empty
  Produced by the form's real submit with nothing typed: the errors sit under their fields, and check that nothing implies the credentials were wrong — nothing was sent.
- `not-found` — Route not found · `#/projects/nope`
  The router's catch-all for a hash nothing owns: check it stays inside the shell and offers the overview, because a panel that 404s to a blank page has lost the operator twice.

## Feedback — 11 states

- `feedback-loading-first` — Feedback — loading, first paint · `#/design/states` · *(feedback)* · forced by ui:states.section="loading-first"
  What a screen shows before any data exists: check the skeleton occupies the content's real shape, because a first paint of nothing is how a slow panel becomes a judged one.
- `feedback-loading-actions` — Feedback — loading, actions and refetches · `#/design/states` · *(feedback)* · forced by ui:states.section="loading-actions"
  Buttons that spin without blanking their card: a refetch must keep the old data readable while it checks, or every refresh feels like a crash.
- `feedback-empty` — Feedback — empty states · `#/design/states` · *(feedback)* · forced by ui:states.section="empty"
  Three empties side by side (Projects, Composes, Variables), each selling its own next action: check no two of them are interchangeable, because "nothing yet" means something different in each.
- `feedback-empty-filtered` — Feedback — filtered to nothing · `#/design/states` · *(feedback)* · forced by ui:states.section="empty-filtered"
  The empty that quotes your query back: a filtered zero must read as "no match for this", distinct from the true empty beside it.
- `feedback-error-inline` — Feedback — inline error, with a way out · `#/design/states` · *(feedback)* · forced by ui:states.section="error-inline"
  A failure that keeps the page around it: check the retry is the offered way out and the rest of the screen stays usable, which is what separates an inline error from a page-level one.
- `feedback-error-page` — Feedback — page-level error · `#/design/states` · *(feedback)* · forced by ui:states.section="error-page"
  The screen-size failure ("Could not contact the panel"): check it says what to do next in plain words, because this state is reached exactly when nothing else on the page can help.
- `feedback-form-validation` — Feedback — form validation · `#/design/states` · *(feedback)* · forced by ui:states.section="form-validation"
  Errors painted on a real form (the gallery's demo submit): check each message sits under its own field and names the rule, not just "invalid".
- `feedback-toast` — Feedback — outcome toasts · `#/design/states` · *(feedback)* · forced by ui:states.section="feedback-toast", after:toast:states.deployed
  The app's own success copy held open past its 3 s timeout through the toast adapter: check a toast says what happened and where to find it, and that it can be dismissed.
- `feedback-notification` — Feedback — keyed notification · `#/design/states` · *(feedback)* · forced by ui:states.section="feedback-notification", after:toast:states.deployNotification
  The two-beat deploy notification (started, then finished under the same key): check the second beat replaces the first instead of stacking, which is what the key buys.
- `feedback-destructive` — Feedback — destructive confirmations · `#/design/states` · *(feedback)* · forced by ui:states.section="destructive", ui:states.dangerOpen=1
  The typed-name modal over its demo copy: check the consequence sentence and the disabled-until-match OK are one pattern, because every destructive surface in the panel inherits it.
- `feedback-narrow` — Feedback — narrow viewports · `#/design/states` · *(feedback)* · forced by ui:states.section="narrow"
  The gallery's own 360 px demo, framed at desktop width: it documents what phone widths must answer, and stays a desktop frame until ticket 03 designs the real 390 scenes.

