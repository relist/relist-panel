# 01 — Frameless variables table (Compose page) and secret-preserving paste (everywhere)

Status: ready-for-agent

**What to build:** First read `../spec.md`, in particular Decisions: "A flat variable table on the
Compose page", "The Environment's list is unchanged" and the paste secret-flag rule. Also read Ant Design's `llms.txt` (`Table`, `Tooltip`)
and `docs/GUIDELINES.md` §4.5.

In `web/src/components/VariablesPanel.tsx` (`VariablesEditor`, used by the Environment list, Compose
Variables and set entries):

- Add an **opt-in** frameless rendering (a prop) for use inside a card or tab. Compose Variables and
  set entries use it; the Environment's `VariablesPanel` does **not**. Its rendering, tag column,
  always-visible actions, always-shown filter and header Paste .env button stay exactly as today.
- The frameless rendering has no `Card`, no title, no description and no header buttons: only the
  table, then a row of link buttons ("+ Add variable", or a label the caller passes such as
  "+ Add entry", and "Paste .env" when `onReplace` is given).
- Frameless columns: **Key**, **Value** (`ValueCell`, unchanged), **Updated** only when rows carry
  `updatedAt` (as today), then row actions. Remove the `secret`/`plain` tag column.
- Frameless row actions (Edit, Delete `Popconfirm`) are hidden (`opacity: 0`) until the row is hovered or an
  action has focus (`:focus-within`). Under `@media (hover: none)` they are always visible. Keep
  their `aria-label`s.
- Frameless: show the key filter only when the list has more than 10 rows.
- Frameless: an empty list hides the table header and shows one secondary line from `emptyDescription`, with
  no illustrated `EmptyState` inside the frameless rendering.
- Paste: apply a new pure helper `pasteSecretFlags(current, rows)` in
  `web/src/components/variables-view.ts`. A key already in `current` keeps its `secret`; a new key
  gets `isSecretKey(key)`. This replaces the `secret: false` in `replaceAll`, so it applies to
  every list, **the Environment's included**. It is a bug fix: re-pasting the Environment's `.env`
  unmasks its secrets today.
- Keep the existing props working, or change the three callers in this ticket if the shape changes.

**Blocked by:** —

- [ ] Frameless rendering has no nested card, title or description, and no secret/plain column
- [ ] Environment list renders as before (its existing tests pass unchanged)
- [ ] Row actions hidden at rest, visible on hover, keyboard focus, and at phone width
- [ ] Frameless filter only above 10 rows
- [ ] `pasteSecretFlags` unit-tested; Environment paste keeps an existing secret secret and makes a new `*_PASSWORD` secret (new component test)
- [ ] `pnpm --dir web typecheck`, `test`, `build` clean; no test deleted without a replacement

## Comments
