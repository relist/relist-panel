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

**Status:** ready-for-agent

- [ ] Create modal: the toggle defaults to the heuristic's answer for the typed key and is
      saved with the variable
- [ ] Edit modal: the toggle shows the stored value and saves changes
- [ ] The tag and masking follow the flag alone — a backfilled `JWT_SECRET` masks without any
      client-side guessing; `isSecret` has no heuristic fallback left
- [ ] Bulk paste still creates plain variables; toggling a pasted row updates the flag
- [ ] `pnpm --dir web typecheck`, `test`, `build` green; variables-view tests updated (the
      flagless-fallback case is deleted with the fallback)

## Comments

Cut 2026-09-25. If the toggle's default-on heuristic feels noisy in the render checks (a key
containing `KEY` defaulting to secret when the user meant plain), that is the design working —
the user overrides it once and the guess never runs again.
