# 08 — Variables, reshaped

**What to build:** The Variables tab per the design (GUIDELINES §4.5): values masked by default with
reveal and copy, the `secret` / `plain` tag, key filter, create/edit/delete, and the bulk `.env`
paste with its live parse preview, duplicate-key error and "replace N variables with M" confirmation.
Replaces the monolith's `EnvironmentVariablesPanel`.

**Blocked by: 01, 02, 03, 04, 06**

**Status:** closed

- [x] Masking, reveal and copy work per row; the `secret` tag renders as designed
      (the API has no flag yet — §11 — so the tag is client-side for now, and the ticket notes that)
- [x] Key validation matches the server's `[A-Za-z_][A-Za-z0-9_]*`
- [x] Bulk paste parses, previews, refuses duplicates, and confirms before replacing
- [x] Delete uses the design's confirmation level (Popconfirm for a single variable)
- [x] No feature lost against the monolith's variables panel

## Comments

Implemented in this commit (`feat: rebuild the Variables tab`).

- `components/VariablesPanel.tsx` is the prototype's panel ported onto the real store: values
  masked until revealed with per-row copy (`ValueCell`), the `secret` (gold, filled) / `plain`
  (outlined) tag, a key filter that stays put when it matches nothing ("No keys match …", §8), the
  create/edit modal with the design's key rule, `Popconfirm` delete ("Composes pick this up on
  their next deploy"), the standing deploy-timing paragraph + the SQLite/mode-600 info Alert, and
  the "N variables · applied to M Composes" footer. `EmptyState` offers both fill actions (paste,
  add). Decisions live in a pure module, `components/variables-view.ts` (beside the panel, since
  the full screen editor's rail shares it): `parseDotEnv` (comments, blanks, `export`, quote
  stripping — verbatim from the prototype), `duplicateKeys`, `invalidKeys`, `VARIABLE_KEY_PATTERN`,
  `isSecret`, `maskedValue`, `visibleVariables` and `replacePlan`; 18 new tests, 70 total.
- The `secret` marking is client-side, as §11 anticipates: `isSecret(variable)` reads the API flag
  when it is ever set and otherwise guesses conservatively from the key's shape (`secret`,
  `password`, `token`, `credential`, `key` on its own or at an underscore boundary — so
  `JWT_SECRET` masks and `MONKEY_PATCH` does not). The full screen editor's rail now reads the same
  helper (ticket 07's Comments deferred exactly this), so masking and tags agree across the panel
  and the rail. When the API grows the boolean it wins over the guess in both places.
- Key validation: the create/edit form enforces `[A-Za-z_][A-Za-z0-9_]*` (shared
  `VARIABLE_KEY_PATTERN`) with the design's copy. Note for a human: the **server** currently
  accepts any non-empty key ≤255 chars (`internal/environmentvariables.validateInput` has no
  pattern) — the pattern is the design's rule, enforced client-side only until the backend grows
  it. The bulk paste refuses such keys too: keys a Compose file cannot reference as `${KEY}` block
  the replace with their own error Alert, next to the design's duplicate-key error — without this,
  a paste could create variables nothing can read.
- The bulk modal parses live, previews key/value rows, and its OK button **is** the confirm:
  `Replace N variables with M` (disabled while empty, duplicated or invalid). Below the preview the
  plan names its consequences ("1 added · removes DATABASE_URL, JWT_SECRET", §9). Replacing runs
  through the store's `replaceVariables` (no bulk endpoint: delete dropped, upsert brought, re-read
  — verified call-by-call) behind a keyed notification (§8): a loading line replaced by the result.
  Both modals keep their failure next to the form as an Alert instead of the prototype's unhandled
  rejections; a delete failure joins the screen's error Alert via `onError`.
- The variables load is the page's (`ProjectPage`, per the ticket-07 pattern): the tree ships them
  unloaded, so the active Environment's list is fetched on mount and on switch, feeding the tab
  count as well; the panel renders a centered Spin until then instead of flashing the empty state.
- Monolith parity: create, edit, delete (monolith had no masking, no filter, no bulk paste — all
  added), server errors surfaced (load → the page's Alert; saves → the modal), loading states on
  every action button via the store's pending keys, and value visibility preserved through
  reveal/copy. Nothing from the monolith's panel was dropped.
- Verification: `pnpm --dir web typecheck`, `test` (70), `build` and `go test ./...` green; the
  embedded dist rebuilt. Render checks ran the built bundle through the file:// CDP harness
  (route-shaped paths under the build's base + `Page.setDocumentContent`, a stateful in-page fetch
  shim so mutations are visible to the store's re-reads, one Chrome per scenario): masked table
  (4 rows key-sorted, secret rows `••••` with reveal + copy, plain rows clear, tags right, footer),
  reveal → hide round trip, filter narrowing then a no-match that keeps the filter, create
  (POST, `Added SMTP_PASSWORD`, row tagged secret, tab count 5) and the invalid key blocked by the
  form, edit (PUT, `Saved API_TOKEN`), delete (Popconfirm copy, DELETE, row gone, footer 3), bulk
  (duplicates + invalid keys block with both Alerts and a disabled OK; the clean paste previews
  `Replace 4 variables with 3`, names the removals, and the replace hits the API in the store's
  order and re-renders), the staging Environment's empty state (both actions; its paste modal opens
  at `Replace 0 with 0`, disabled), and the fullscreen rail masking/revealing with the tab. No
  scroll containers added; the page scrolls as one.
- For a human: the server-side key pattern and the API's `secret` flag are the two §11 gaps —
  until they land, the pattern is client-side only and the tag/masking is the key-shape heuristic
  described above.

Follow-up (2026-09-25, orchestrator): the flagged server gap is closed — `validateInput` now
enforces `[A-Za-z_][A-Za-z0-9_]*` (unit-tested), so the client-side rule this ticket shipped is the
server's rule, not a substitute for it.
