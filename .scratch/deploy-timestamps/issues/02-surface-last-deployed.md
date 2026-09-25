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

**Status:** ready-for-agent

- [ ] `deployedAt` map, its ref, and every read of them are gone; `git grep deployedAt` finds only
      the API field's wire name
- [ ] Overview's "Last deployed" fills from the API; a deploy made in a previous session shows
- [ ] The attention badge and the Overview list agree on state-based degrades (partial,
      stopped-after-success) without either reading a session stamp
- [ ] The Monitor tab's deploy marker appears on all four charts when the (now server-recorded)
      last deploy falls inside the window, and is absent when it does not — including across a
      page reload
- [ ] `pnpm --dir web typecheck`, `test`, `build` green; overview-view's `withDeployStamps` tests
      are removed with the helper or rewritten against plain tree values

## Comments

Cut 2026-09-25. Note the store's pending-key vocabulary does not change — `deploy:*` stays as is;
only the stamping goes. The ServiceMonitor's "Last deploy not recorded" empty-marker state becomes
reachable only for a genuinely never-deployed Compose, which shares the tab's existing empty
state; if the copy needs adjusting to match, do it and note it here.
