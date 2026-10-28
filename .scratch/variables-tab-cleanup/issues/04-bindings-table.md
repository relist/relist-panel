# 04 — Service bindings card as a table

Status: closed

**What to build:** First read `../spec.md`, Decision "Service bindings card as a table".

In `web/src/components/ComposeBindingsCard.tsx`:

- The card is `size="small"`, titled "Service bindings" with the ⓘ tooltip from the spec. Remove the
  description text.
- The live rows become a small `Table` with these columns:
  - **Service**, in the code font.
  - **Sets, in order**, holding the existing multi-select with `variant="borderless"`. Keep the
    draft, serialised-save and reload logic as it is.
  - An icon-only Effective env button: `ThunderboltOutlined`, `type="text"`, tooltip "Effective env",
    `aria-label="Effective env for <service>"`. It opens the unchanged `EffectiveEnvModal`.
- Stale rows behave as today: listed after the live rows with a warning icon, the bound set names as
  text, and **Remove** only.
- Empty states are unchanged.

**Blocked by:** —

- [x] Table layout; Effective env is an icon button with the accessible name `Effective env for <service>`
- [x] Rapid-pick binding and stale Remove still work
- [x] `ComposeBindingsCard.test.tsx` queries updated (`/Effective env for web/`); nothing deleted
- [x] `pnpm --dir web typecheck`, `test`, `build` clean

## Comments
