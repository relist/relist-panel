# Spec — Compose Variables tab: sets as tabs, quieter cards, Paste .env

Date: 2026-10-28.

Frontend only. No backend, API, schema or ADR change. Builds on ADR-0015 and
`.scratch/scoped-variables/spec.md`, whose domain model is unchanged.

Before touching `web/`, read Ant Design's [`llms.txt`](https://ant.design/llms.txt) and the
component pages it links for `Tabs`, `Table`, `Tooltip` and `Popconfirm`, checked against the
installed `antd` (6.5.1, `web/package.json`), and `docs/GUIDELINES.md`. `tabPosition` is deprecated
in antd 6; it is not needed here.

## Problem

The Compose page's Variables tab (`web/src/components/ComposeVariablesTab.tsx`) is hard to scan:

- **Nesting.** Expanding a Variable Set shows a full `VariablesEditor` card inside a `Collapse`
  panel inside the Variable Sets card: three frames, a second title ("Entries · db-credentials"),
  a second description, a second filter box and a second primary button.
- **Repeated text.** "Changes take effect on the next deploy" is in every card and every set,
  although the `RedeployBanner` above already says it. Each card opens with a one- or two-line
  description before its content.
- **Uneven rows.** A Compose Variable that overrides an Environment Variable shows
  `overrides production: info` as a second line under the value.
- **Heavy bindings card.** Each Service row has a full-size "Effective env" button.
- **No paste-in.** Operators already hold `.env` files, but only the Environment's list accepts
  a pasted `.env` (`onReplace` is passed for the Environment only). Compose Variables and set
  entries must be typed one by one.

## Goal and success criteria

- The Variable Sets card shows **one tab per set** (Ant Design `Tabs`, default `line` type), not a
  `Collapse`. The active tab's content is a read-only line naming the Services that bind the set, then a
  flat entries table. Binding stays in the Service bindings card only.
- No card contains another card.
- The "next deploy" sentence appears only in the `RedeployBanner`. Card explanations live in an
  ⓘ tooltip beside the card title.
- Every row of a variable table on the Compose page is one line high.
- The Environment's Variables list looks and behaves as today, except for the paste secret fix.
- **Paste .env** is available for Compose Variables and for each set's entries, with the same
  parse, preview and replace confirmation the Environment uses.
- Behaviour that exists today keeps working: create, edit, delete, secret masking, reveal, copy,
  rename and delete a set, bind, unbind and order sets, stale bindings, Effective env, redeploy
  banner.

## Out of scope

- The service-first layout and the "Add to `<service>`" shortcut that creates a set. These were
  prototyped (branch `prototype/service-env`, `?variant=A|B|C` at `/prototype-service-env.html`) and
  were not chosen.
- Putting Compose Variables and sets in one tab row (prototype variant W). It was rejected because it
  puts two kinds of thing side by side as if they were the same. Compose Variables are usable as
  `${VAR}` and reach every container. Sets are neither.
- A bulk API. Paste is composed from the existing per-row calls, as it already is for the
  Environment (see Decisions).
- Drag-and-drop ordering of bindings.
- Linking a set tab in the URL.

## Prototype

Branch `prototype/service-env` (never merged), commits `5f8387b`, `4880645` and `fa5205b`. Run it with
`git switch prototype/service-env && pnpm --dir web proto:sets-tabs`, then open
`/prototype-sets-tabs.html?variant=Y`. Variant **Y** is the chosen design. Variant X imitates
today's layout for comparison. Variants Z (side tabs) and W (one tab row) were not chosen. The
prototype code is throwaway: rebuild the design in the real components; do not copy the prototype
code.

## Decisions

**Sets as `line` tabs.** The user chose the default tab style over `editable-card`. As a result the
tab bar has no built-in add or close buttons:
- **New set** is a small default button in the Variable Sets card's `extra`. It opens the existing
  name form (same rules: `^[a-z][a-z0-9-]{0,62}$`). After a create, the new set's tab is active.
- **Rename** and **Delete** apply to the active set and sit in the tab bar's
  `tabBarExtraContent`. Both are small `text` buttons with an icon, and Delete is `danger`.
- Rename opens a small modal with the same name field and rules. It replaces today's in-place
  rename in the Collapse header.
- Delete keeps today's `Popconfirm` and wording (`deleteSetNote` in
  `web/src/components/bindings-view.ts`): "Unbinds it from api, worker. Takes effect on the next
  deploy." or "Its entries are deleted with it." After a delete, the first remaining tab is active.
- A tab label is the set name in the code font, followed by the entry count in secondary text,
  e.g. `db-credentials 3`. Ant Design's overflow menu handles more tabs than fit.
- With no sets, the card shows today's `EmptyState` with one **New set** action, and no `extra`
  button (one add button, as now).
- The active tab is component state. When the list reloads, the active set stays selected while it
  still exists; otherwise the first tab is selected.

**Binding only in the Service bindings card.** A set tab has no binding control (decided
2026-10-28: an editable "Given to" select was prototyped and dropped). This keeps one place to
bind, unbind and order sets. The top of each tab shows the set's `boundServices` as read-only text:
"Bound to worker, db" with the Service names in the code font. A set with no bindings shows
"Not bound to any Service yet", and its tab label is not changed. This replaces the "bound to" tags
of today's Collapse header.

**A flat variable table on the Compose page.** `VariablesEditor` (`web/src/components/VariablesPanel.tsx`)
gets a frameless rendering for use inside a card or tab, used by Compose Variables and set entries
only. Its title, description, card frame and
header buttons are left out, and it shows only:
- The table, with columns **Key**, **Value** and row actions. The value cell is `ValueCell` as
  today: masked when secret, with reveal and copy. The **secret/plain** tag column goes: the mask
  and the eye button already show whether a value is secret. The **Updated** column stays only
  where rows carry `updatedAt` (the Environment list), as today.
- **Row actions on hover.** Edit and Delete are `opacity: 0` until the row is hovered or one of
  them has keyboard focus (`:focus-within`). On devices without hover (`@media (hover: none)`) they
  are always visible. They keep their `aria-label`s, so tests and screen readers find them as
  before.
- **Below the table, two link buttons:** "+ Add variable" ("+ Add entry" for a set), and
  "Paste .env" when `onReplace` is given.
- **The key filter** appears only when the list has more than 10 rows.
- The header is hidden when the list is empty, and the empty text is one short secondary line,
  not an illustrated `EmptyState`.

**The Environment's list is unchanged** (decided 2026-10-28). `VariablesPanel` in the same file,
on the Project page's Variables tab (GUIDELINES §4.5), keeps today's rendering: its card, title,
notice, standing note, the secret/plain tag column, always-visible row actions, the always-shown
filter and the Paste .env button in the card header. The only change there is the paste secret fix
below. Implement the frameless rendering as an opt-in prop, so the Environment's call site and output
stay as they are.

**Override shown as a tag.** A Compose Variable whose key is also an Environment Variable shows a
`Tag` "overrides production" next to its value, on the same line. The tag's tooltip shows the
Environment value, as `••••` when that variable is secret. The text comes from `overrideHint` and
`overriddenVariable` in `web/src/components/variables-view.ts`, adjusted so the tag and the tooltip
each get their part.

**Paste .env for Compose Variables and set entries.** Both use the existing bulk modal in
`VariablesEditor`: textarea, live parse with `parseDotEnv`, duplicate and invalid key errors, the
`replacePlan` summary, and the "Replace N variables with M" confirm. Keys missing from the paste are
deleted.
- Store (`web/src/data/store.tsx`): add `replaceComposeVariables(composeId, rows)` and
  `replaceSetEntries(composeId, setId, rows)`, composed like `replaceVariables`. They delete the keys
  the paste drops, update the keys it keeps, create the keys it adds (using `saveComposeVariable`,
  `deleteComposeVariable`, `saveSetEntry` and `deleteSetEntry`, or the `api` calls behind them),
  then reload once. Pending keys are `bulk-compose-variables:<composeId>` and
  `bulk-set-entries:<setId>`.
- Not atomic, as for the Environment today: a failure partway leaves the rows written so far. The
  modal shows the error and the list reloads, so the operator sees the real state and can paste
  again.
- **The secret flag on paste** is fixed for all three lists, the Environment's included (decided
  2026-10-28, part of this work). Today every pasted row lands plain. With
  this change, a key that already exists keeps its current flag, and a new key takes `isSecretKey`'s
  answer. Today's behaviour would silently unmask secrets on every re-paste.
- After a replace, the tab reloads its data (the variables list or the sets), so the redeploy banner
  updates as it does after a single write.

**Card titles with ⓘ.** Each of the three cards is a `size="small"` `Card` whose title is the name
plus an `InfoCircleOutlined` `Tooltip`:
- Compose variables: "Only for this Compose, usable as ${VAR} and given to every container. A key
  that is also an Environment Variable overrides it here."
- Variable Sets: "Named groups of variables for the Services you bind them to. Not usable as
  ${VAR}."
- Service bindings: "Order matters: a later set overrides an earlier one for the same key."

No card has a description paragraph and none repeats "next deploy".

**Service bindings card as a table.** A small `Table` with columns **Service** (code font) and
**Sets, in order**, which holds the existing multi-select, `variant="borderless"`, with today's
draft and serialised-save logic in `ComposeBindingsCard` unchanged. The last column is an icon-only
button (`ThunderboltOutlined`, `type="text"`) with tooltip "Effective env" and
`aria-label="Effective env for <service>"`, which opens the unchanged `EffectiveEnvModal`.
- Stale bindings stay as today: listed after the live rows, the Service name with a warning icon,
  the bound set names as text, and a **Remove** button only.
- The empty states are unchanged.

## Files

- `web/src/components/VariablesPanel.tsx`: opt-in frameless rendering (hover actions, conditional
  filter, links under the table, no secret/plain column) and secret-preserving paste for every list.
- `web/src/components/ComposeVariablesTab.tsx`: card titles with ⓘ, the override tag, and
  `onReplace` for Compose Variables.
- `web/src/components/VariableSetsCard.tsx`: rewrite from `Collapse` to `Tabs` (New set in
  `extra`, Rename/Delete in `tabBarExtraContent`, read-only "Bound to" line, frameless entries table
  with `onReplace`).
- `web/src/components/ComposeBindingsCard.tsx`: table layout and icon-only Effective env button.
- `web/src/components/variables-view.ts`: helpers for the override tag and tooltip, and a pure
  `pasteSecretFlags(current, rows)` that applies the paste secret rule. Both are unit-tested.
- `web/src/data/store.tsx`: `replaceComposeVariables` and `replaceSetEntries`.
- `docs/GUIDELINES.md`: §4.5 (a paste keeps existing secret flags and guesses new ones) and the
  §4.6 Variables tab text (three cards as described here; sets as tabs; paste-in for Compose
  Variables and sets, replacing "no paste-in").
- Rebuilt embedded frontend (`task build-web`), committed separately.

## Tests

Update the existing tests; do not delete one unless a new test covers the same behaviour.

- `VariableSetsCard.test.tsx`: tabs render one per set with the entry count; switching tabs shows
  that set's entries; New set activates the new tab; Rename through the modal; Delete through the
  `Popconfirm` with the bound-Services text; the "Bound to" / "Not bound" line; empty state with a
  single add button.
- `ComposeVariablesTab.test.tsx`: the override tag and its tooltip text, masked when the
  Environment variable is secret; Paste .env replaces Compose Variables.
- `ComposeBindingsCard.test.tsx`: the Effective env queries move to the new accessible name
  (`/Effective env for web/`); stale rows are unchanged.
- `variables-view.test.ts`: `pasteSecretFlags` and the override tag helpers.
- New (no paste test exists today): pasting into the Environment list keeps an existing secret key secret and creates a `*_PASSWORD` key as secret.
- The Environment list's existing tests pass unchanged (its rendering does not change).
- Row actions are still found by `getByRole('button', { name: 'Edit KEY' })`. jsdom ignores
  opacity, so no test change is needed for hover.

## Tickets

In `issues/`, one file per ticket:

1. `01-variables-table.md`: the frameless `VariablesEditor` table, hover actions, the filter
   threshold, no secret/plain column (opt-in, Compose page only), and secret-preserving paste for
   every list, the Environment's included.
2. `02-compose-variables-card.md`: the ⓘ title, the override tag, and Paste .env for Compose
   Variables. Blocked by 01.
3. `03-sets-as-tabs.md`: `VariableSetsCard` as tabs, the read-only "Bound to" line, the rename modal, and Paste .env
   for entries. Blocked by 01.
4. `04-bindings-table.md`: `ComposeBindingsCard` as a table with an icon Effective env button.
5. `05-docs-and-build.md`: GUIDELINES §4.5 and §4.6, and the embedded frontend rebuild. Blocked by
   01–04.

## Verification

- `pnpm --dir web typecheck`, `pnpm --dir web test`, `pnpm --dir web build`.
- In a browser at desktop and phone width: with 0, 1 and more than 6 sets (overflow menu); paste
  into Compose Variables and into a set; the redeploy banner appears after each; row actions are
  visible without hover at phone width.
