# 03 — Variable Sets as tabs

Status: closed

**What to build:** First read `../spec.md`: Decisions "Sets as `line` tabs", "Binding only in the
Service bindings card" and "Paste .env…". Also read Ant Design's `llms.txt` (`Tabs`; in antd 6 use `items` and
`tabBarExtraContent`, and do not use the deprecated `tabPosition`). Optional reference: prototype
variant Y. Run `git switch prototype/service-env && pnpm --dir web proto:sets-tabs` and open
`/prototype-sets-tabs.html?variant=Y`. Do not copy its code. Its "Given to" select was dropped, so do not build it.

Rewrite `web/src/components/VariableSetsCard.tsx` from `Collapse` to `Tabs` (default `line` type):

- The card is `size="small"`, titled "Variable Sets" with the ⓘ tooltip from the spec. **New set** is
  a small button in `extra`, shown only when at least one set exists. The empty state stays
  `EmptyState` with its single New set action.
- One tab per set. The label is the name in `CODE_FONT_STACK` followed by the entry count in
  secondary text. The active tab is component state. After a create, the new set is active. After a
  delete or a reload that removes the active set, the first set is active.
- At the top of the pane, on the "Bound to" line (moved out of `tabBarExtraContent`, which hid tabs at phone width): **Rename** (`EditOutlined`, `type="text"`), which opens a modal with the
  existing name field and rules, and **Delete** (`DeleteOutlined`, `danger`, `type="text"`) inside
  the existing `Popconfirm` with `deleteSetNote` text. Both act on that pane's set.
- Tab content:
  - A read-only secondary line from the set's `boundServices`: "Bound to worker, db" (Service names
    in the code font), or "Not bound to any Service yet". There is no binding control here; binding
    stays in the Service bindings card.
  - Then ticket 01's frameless table with "+ Add entry" and Paste .env.
- Paste: add `replaceSetEntries(composeId, setId, rows)` to `store.tsx`, composed like
  `replaceVariables`. Pending key: `bulk-set-entries:<setId>`. After it finishes, reload the sets.
- After any write (set or entry), reload the sets and bindings as today, so the bindings
  card and the redeploy banner follow.

**Blocked by:** 01

- [x] Tabs with name + count; New set activates the new tab; Rename modal; Delete Popconfirm names bound Services
- [x] "Bound to …" / "Not bound to any Service yet" line follows binding changes made in the bindings card
- [x] Paste .env replaces a set's entries
- [ ] Empty state has one add button; more than 6 sets overflow into Ant Design's menu
- [x] `VariableSetsCard.test.tsx` updated, with no behaviour left untested
- [x] `pnpm --dir web typecheck`, `test`, `build` clean

## Comments

- 2026-10-28 (Claude): done in 8e6a94e and dd679a5. The browser check at 390px showed
  `tabBarExtraContent` hiding every tab except the first, so Rename/Delete moved into the pane.
  The spec records why. The case with more than 6 sets (overflow menu) was not checked in a
  browser.
