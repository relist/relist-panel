# 07 — Compose-owned Variable Sets: UI

**What to build:** Read `../spec.md` (revision note, UI section), Ant Design's `llms.txt` and
`docs/GUIDELINES.md`. Consume the API from ticket 06.

- **Environment's Variables tab** (`web/src/pages/ProjectPage.tsx`): remove `VariableSetsCard`.
  Keep the Environment Variables card and its "Used by every Compose · usable as `${VAR}`" note.
- **Compose page Variables tab** (`web/src/components/ComposeVariablesTab.tsx`): three cards in this
  order — Compose variables, **Variable Sets**, Service bindings.
  - Variable Sets card: adapt the existing `VariableSetsCard` to the Compose (props/route change,
    not a copy). Rows show name, entry count and "bound to" Service tags from `boundServices`.
    Expanding shows the shared `VariablesEditor`. "New set" name form; rename in place; delete uses a
    confirmation that names the bound Services ("Unbinds it from api, worker. Takes effect on the
    next deploy.") — there is no 409 any more.
  - Service bindings card: its select lists this Compose's sets. When the Compose has no sets, the
    empty state says to create one in the Variable Sets card above.
  - After set create/rename/delete and entry writes, the bindings card and the Compose (banner)
    refresh; the old cross-Compose `refreshSetBinders` re-listing is no longer needed — replace it
    with a refresh of this Compose.
- Update `web/src/data/api.ts`, `store.tsx`, `types.ts` for the new routes and `boundServices`;
  remove what only served Environment-owned sets (`usedBy`, `ApiError.usedBy` if now unused).
- Rebuild the embedded frontend (`task build-web`) and commit it separately.

**Blocked by: 06**

**Status:** closed

- [ ] Environment's Variables tab shows no Variable Sets card
- [ ] Compose Variables tab: create, rename, delete (with the bound-Services confirmation) and edit entries of a set; secret masking works
- [ ] Binding select lists only this Compose's sets; binding, unbinding and the rapid-pick fix still work
- [ ] Redeploy banner appears after an entry edit of a bound set and after deleting a bound set; not after create/rename
- [ ] Empty states: no sets → sets card empty state + one add button; bindings card points to the sets card
- [ ] Desktop and phone width checked (throwaway harness outside the repo is fine)
- [ ] `pnpm --dir web typecheck`, `test`, `build` clean; tests updated, none deleted without a replacement covering the same behaviour

## Comments
