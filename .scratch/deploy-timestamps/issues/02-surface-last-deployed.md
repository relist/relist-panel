# 02 — Surface `lastDeployedAt` everywhere; retire the session stamps

**What to build:** The frontend switches to the API's `lastDeployedAt` as the only source of
deploy time, and the store's `deployedAt` session map plus its ref mirror are deleted. Consumers
to update: the Overview's "Last deployed" column and its attention derivation
(`withDeployStamps` in `web/src/pages/overview-view.ts` collapses — the tree values pass through
unchanged), the shell's attention badge (`AppShell.tsx` reads the API field, not the map), and
the Monitor tab's deploy marker (`ServiceMonitor` — the marker now survives a refresh and appears
for deploys made before the session). The store's `deployCompose` no longer stamps anything; a
deploy still refreshes the tree, which now carries the timestamp itself.

**Blocked by: 01**

**Status:** closed

- [x] `deployedAt` map, its ref, and every read of them are gone; `git grep deployedAt` finds only
      the API field's wire name
- [x] Overview's "Last deployed" fills from the API; a deploy made in a previous session shows
- [x] The attention badge and the Overview list agree on state-based degrades (partial,
      stopped-after-success) without either reading a session stamp
- [x] The Monitor tab's deploy marker appears on all four charts when the (now server-recorded)
      last deploy falls inside the window, and is absent when it does not — including across a
      page reload
- [x] `pnpm --dir web typecheck`, `test`, `build` green; overview-view's `withDeployStamps` tests
      are removed with the helper or rewritten against plain tree values

## Comments

Cut 2026-09-25. Note the store's pending-key vocabulary does not change — `deploy:*` stays as is;
only the stamping goes. The ServiceMonitor's "Last deploy not recorded" empty-marker state becomes
reachable only for a genuinely never-deployed Compose, which shares the tab's existing empty
state; if the copy needs adjusting to match, do it and note it here.

## Comments (implementation)

Implemented in this ticket's own commit, `feat: surface api deploy times everywhere` (ticket 01
landed as `7c82d00`). The store's `deployedAt` state, ref, `stampDeployed` and its call in
`runComposeAction` are deleted (the `deploy:*` pending key and `stampChecked` are untouched — a
deploy's tree patch now carries the API's fresh `lastDeployedAt`); `withDeployStamps` collapsed
out of `overview-view.ts` (`flattenComposes(projects)` is the whole pipeline); the AppShell badge
and the Overview derivation read `compose.lastDeployedAt` directly; `ServiceMonitor`'s marker and
caption read `compose.lastDeployedAt`. `Compose.lastDeployedAt` and the status primitives widened
to `string | null` — the API writes the key as JSON `null` for never-deployed, and `composeState`
treats null exactly like absent (new `types.test.ts` case).

- Copy adjustments, both noted per the ticket: the Overview em-dash tooltip now reads "This
  Compose has never been deployed" (it said the API does not report deploy times — no longer
  true); the ServiceMonitor's "Last deploy not recorded" caption is kept verbatim — it is now
  reachable only where services exist but no stamp is recorded (deployed before the panel began
  recording, the migration leaves those NULL), where "not recorded" is literally accurate, while
  a genuinely never-deployed Compose renders the tab's "Nothing to monitor yet" empty state
  instead (no containers → no Service rows).
- Verification: `pnpm --dir web typecheck`, `test` (132), `build` green, embedded dist rebuilt
  (the new bundle contains `lastDeployedAt` only — zero standalone `deployedAt` occurrences), and
  `go test ./...` / `go vet ./...` green against the fresh dist. Render checks ran the built
  bundle through the design-port file:// CDP harness (route-shaped file paths, method-aware fetch
  shim over fixtures, one Chrome process per scenario), 13/13: Overview column "8 minutes ago"
  from a pre-session deploy and "2 hours ago" on the stopped-after-success Compose, em-dash for
  the null-stamped one, badge=1 agreeing with the attention alert and list (never-deployed not
  counted, no session deploy in the scenario); Monitor tab marker `data-markers=1` on all four
  charts with the stamp inside the 1h window and `0` on all four with it 3h outside, caption
  "Deployed 8 minutes ago" / "Deployed 3 hours ago" off the API field on a fresh page load (i.e.
  across a reload), and the never-deployed Compose hitting the tab empty state with no caption
  leak.
- Deviation: none beyond the copy notes above. Screenshots for a human: `/tmp/dt-harness/
  monitor-{inside,outside}.png` (harness scratch outside the repo; the Overview scenario is fully
  covered by its DOM assertions).
