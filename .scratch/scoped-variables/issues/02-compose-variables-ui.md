# 02 — Compose Variables: Compose page Variables tab

**What to build:** Read `../spec.md` (UI section) and, per AGENTS.md, Ant Design's `llms.txt` plus
`docs/GUIDELINES.md` before touching `web/`. Add a **Variables** tab between Editor and Domains on
`web/src/pages/ComposePage.tsx` with a **Compose variables** card that reuses the editor in
`web/src/components/VariablesPanel.tsx`; generalise it to take its list and CRUD calls as props rather
than copying it. A row whose key is also an Environment Variable shows
"overrides <environment>: <value or ••••>", masking by the Environment Variable's secret flag.

Extend the redeploy banner so it also shows when `variablesChangedAt >= lastDeployedAt` (beside
`domainsPendingRedeploy` in `web/src/pages/domains-view.ts`). The Editor tab's "Environment" card
counts Compose Variables too. Leave a placeholder for the Service bindings card from ticket 04 only if
it falls out naturally; do not build it here.

Rebuild the embedded frontend (`task build-web`) and commit the result separately, as earlier UI
work did.

**Blocked by: 01**

**Status:** closed

- [ ] Add, edit, delete, secret toggle, reveal and copy work exactly as on Environment Settings
- [ ] Override hint appears and disappears as matching Environment Variables come and go
- [ ] Banner appears after a Compose Variable change and clears after deploy
- [ ] Environment Settings' Variables panel is unchanged in behaviour after the refactor
- [ ] Empty, filled and secret-masked states checked at desktop and phone width (16px gutter, no horizontal scroll)
- [ ] `pnpm --dir web typecheck`, `test`, `build` clean; view-logic unit tests for the override hint and banner

## Comments
