# 05 — Auth screens

**What to build:** Setup and sign-in per the design (GUIDELINES §4.7): a single centred card, the
panel's wordmark, the setup flow's password rules and confirm field, and the design's error handling.
Replaces the monolith's `AuthScreen`.

**Blocked by: 01, 02, 03, 04**

**Status:** closed

- [x] `/setup` and `/signin` match the design; setup is reachable only while the API reports
      `setupRequired`
- [x] Field-level validation matches the design's copy; a wrong password renders the design's error
      state, not a toast
- [x] Signing out returns to `/signin` and the shell is gone
- [x] No feature lost against the monolith's auth flow (setup, login, logout, error surfacing)

## Comments

Implemented in this commit (`feat: port the auth screens`).

- `web/src/pages/AuthPages.tsx` ports the prototype's `AuthPages.tsx` markup — split layout with the
  panel pitch in `colorPrimaryBg`, wordmark, and the single outlined card on the right; setup keeps
  its `Account → Ready` steps and ends in the `Result` with the Project → Environment → Compose
  tags. Screens mount outside the shell: `app/routes.tsx` gained a `RequireAuth` layout route (the
  gate moved out of `main.tsx`, which is now theme + store + router only) with `/signin` and
  `/setup` beside it, so a signed-out visitor never sees the sider.
- Entry decisions live in a pure module, `app/entry.ts`: `entryScreen(auth)` is the monolith's
  routing table (`authenticated → app, setupRequired → setup, else signin`), `signInTarget` honours
  the gate's deep-link `state.from` and falls back on anything that is not a panel-absolute path.
  Both unit-tested (5 tests, 29 total). The gate redirects with `state.from`; sign-in returns there.
- Reachability, verified both ways: `/signin` while `setupRequired` redirects to `/setup` (monolith
  first-run parity), and `/setup` once the account exists redirects to `/signin` (the redirect is
  skipped after setup completes so the `Result` survives `setupRequired` flipping off).
- Error handling is the design's: field-level copy from the prototype (email required/valid,
  password required + `Use at least 8 characters`, confirm + `Passwords do not match`; the email
  type rule kept on sign-in too, as the monolith had it), and a rejected login/setup renders an
  error `Alert` on the card — the API's own message ("invalid email or password") — never a toast.
  Pending wiring uses the store's pending keys (`signin`, `signup`).
- Monolith parity: setup does not start a session — the `Result`'s "Open the panel" leads to
  sign-in; the outcome message (`Admin account created`) fires as designed; logout returns to
  sign-in with the shell unmounted.
- Deviations, deliberate: the "First run?" footer link renders only while `setupRequired` (it would
  bounce off `/setup` otherwise); the design's setup `Result` copy replaces the monolith's success
  notice ("Admin account created. Sign in to continue.") — same information, design's words; the
  right-hand panel collapses to the card below the split's 44% only on narrow viewports (prototype
  behaviour, no new responsive rules).
- Verification: `pnpm --dir web typecheck`, `test` (29), `build` and `go test ./...` green; the
  embedded dist rebuilt. Render checks ran the built bundle through the file:// single-file harness
  (per-route copies; `Page.setDocumentContent` keeps the document URL equal to the route so the
  router needs no rewrite, and history rewrites that a file:// document refuses are swallowed — URL
  matching itself is proven by the copies' paths): sign-in, setup form, wrong-password error state,
  first-run redirect, setup success + "Open the panel", `/setup`-when-set-up redirect, unauthenticated
  deep link → sign-in → back to the deep link, and sign-out → sign-in with the shell gone —
  screenshots reviewed against `#/design/…` references. The failed-status Retry card from ticket 04
  is kept in `RequireAuth` (retry button loading via the `auth-status` pending key).
