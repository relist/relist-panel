# 02 — The secret toggle, and the tag reads the flag

**What to build:** `web/src/components/VariablesPanel.tsx`: the create and edit modals gain a
secret toggle that defaults to the existing `isSecretKey(key)` heuristic's answer (the heuristic
stays for exactly this — suggesting a default — and for nothing else). The `secret`/`plain` tag
and the masking decision read the API's flag only: `isSecret()` in `variables-view.ts` loses its
fallback (`variable.secret ?? isSecretKey(...)`) — the flag is always present after ticket 01's
migration, so the guess no longer runs at render time. Bulk `.env` paste creates plain variables
(the paste format carries no flag) exactly as today; a per-row toggle afterwards is the user's
correction path. The fullscreen editor's variables rail (`FullscreenEditor.tsx`) marks rows from
the same flag.

**Blocked by: 01**

**Status:** closed

- [x] Create modal: the toggle defaults to the heuristic's answer for the typed key and is
      saved with the variable
- [x] Edit modal: the toggle shows the stored value and saves changes
- [x] The tag and masking follow the flag alone — a backfilled `JWT_SECRET` masks without any
      client-side guessing; `isSecret` has no heuristic fallback left
- [x] Bulk paste still creates plain variables; toggling a pasted row updates the flag
- [x] `pnpm --dir web typecheck`, `test`, `build` green; variables-view tests updated (the
      flagless-fallback case is deleted with the fallback)

## Comments

Cut 2026-09-25. If the toggle's default-on heuristic feels noisy in the render checks (a key
containing `KEY` defaulting to secret when the user meant plain), that is the design working —
the user overrides it once and the guess never runs again.

Implemented in this commit (`feat: give the variables screen the secret toggle`).

- The toggle follows the typed key live while creating (`onValuesChange` → `isSecretKey`),
  starting from plain, until the user answers it themselves — after that the override survives
  further key edits, and editing never re-suggests (the stored flag is the answer). The heuristic
  now has exactly one job: suggesting that default.
- `isSecret(variable)` is `variable.secret`, and its parameter type is `Pick<EnvironmentVariable,
  'secret'>` — a caller cannot even hand it a key to guess from; typecheck enforces the
  flag's presence everywhere (`secret` became required in `types.ts`, so fixtures that used
  `secret: undefined` had to say what they meant). The flagless-fallback tests went with the
  fallback.
- Bulk paste maps every parsed row to `{ ...row, secret: false }` explicitly — the paste format
  carries no flag, and a replace flattens kept keys to plain (the per-row toggle is the
  correction path, per the spec). `NewVariable`/`EnvironmentVariableInput` carry the flag so the
  wire is always the caller's answer.
- Verified: `pnpm --dir web typecheck` (clean), `test` (133 — the deleted fallback cases account
  for the drop from 134), `build` (embedded dist rebuilt), then `go test ./...` green.
- Render checks, the file:// CDP harness from `.scratch/design-port/issues/07-compose.md`
  (single-file inlined build at base `/tmp/rp-render`, method-aware fetch shim over an in-memory
  store, route-shaped file path + `Page.setDocumentContent` — the extensionless file serves as
  text/plain, which is what setDocumentContent is for; one Chrome process per scenario; real
  `Input.insertText` keystrokes):
  - Panel: backfilled `JWT_SECRET` and a user-flagged `DATABASE_URL` masked with `secret` tags
    and reveal buttons; `TZ` and a deliberately plain `CONNECTION_STRING` (the heuristic's old
    miss) shown unmasked with `plain` tags and no reveal button; reveal → `hunter2` shown →
    hide → masked again.
  - Create modal: toggle off at empty key; `JWT_SECRET`-shaped and `SMTP_PASSWORD`-shaped keys
    default it on; `LOG_LEVEL` defaults it off again (live while typing); a manual override
    survives key edits; the POST body carried `"secret": false` and the new row rendered plain.
    The switch's `aria-checked` and `ant-switch-checked` class both tracked every step.
  - Edit modal: the plain row opens off, toggled on and saved → PUT body `"secret": true`, row
    re-tagged `secret` and masked after the reload; the backfilled row opens checked.
  - Bulk paste: `JWT_SECRET=rotated-by-paste` + `LOG_LEVEL=debug` replaced the set — both rows
    plain (JWT_SECRET's flag flattened by the flagless PUT, value visible unmasked, no reveal
    button), dropped keys' rows gone.
  - Screenshots under `/tmp/rp-harness/shots/` (panel masked/revealed, both modals, after-paste);
    the fullscreen rail was not render-checked (Monaco on file://) — it reads the same
    `isSecret`, now flag-only, and its marking comment is updated.

For a human: nothing outstanding — the flag is display-only by design, so no review of stored
values is needed; the harness lives in `/tmp` and is throwaway.
