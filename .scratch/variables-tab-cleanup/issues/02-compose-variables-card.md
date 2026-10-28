# 02 — Compose variables card: ⓘ title, override tag, Paste .env

Status: closed

**What to build:** First read `../spec.md`: Decisions "Override shown as a tag", "Paste .env for
Compose Variables and set entries" and "Card titles with ⓘ".

- `web/src/components/ComposeVariablesTab.tsx`: wrap the Compose Variables list in a `size="small"`
  `Card` whose title is "Compose variables" plus an `InfoCircleOutlined` `Tooltip` carrying the text
  from the spec. Inside the card, use ticket 01's frameless table. Delete the description paragraph
  and every "next deploy" sentence; the `RedeployBanner` above already covers it.
- Override: replace the `annotate` second line with a `Tag` "overrides <environment>" on the same
  line as the value. Its tooltip shows the Environment value, as `••••` when that variable is secret.
  Split `overrideHint` in `web/src/components/variables-view.ts` (or add a sibling) so the tag text
  and the tooltip text come separately, and keep it unit-tested.
- Paste: add `replaceComposeVariables(composeId, rows)` to `web/src/data/store.tsx`, composed like
  `replaceVariables`: delete the keys the paste drops, update the kept keys, create the new keys,
  then reload once. Pending key: `bulk-compose-variables:<composeId>`. Pass it as `onReplace`, then
  call `onChanged` so the list and the redeploy banner refresh.

**Blocked by:** 01

- [x] Card title with ⓘ tooltip; no description paragraph
- [x] Override tag on one line; tooltip value masked for a secret Environment variable
- [x] Paste .env replaces Compose Variables (adds, updates, removes) and the banner appears
- [x] Tests in `ComposeVariablesTab.test.tsx` updated or added for the tag and paste
- [x] `pnpm --dir web typecheck`, `test`, `build` clean

## Comments
