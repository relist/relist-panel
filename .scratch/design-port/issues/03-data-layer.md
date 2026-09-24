# 03 — Data layer

**What to build:** `web/src/data/types.ts` + `web/src/data/store.tsx` with the prototype's shapes and
method names, but bodies that call the real API — the fetch call sites already exist in
`web/src/main.tsx` (auth status/setup/login/logout at `:109`, `:153`, `:174`; projects at `:394`,
`:433`, `:456`; environments at `:597`, `:648`, `:672`; variables at `:850`, `:899`, `:922`; composes
at `:1078`, `:1123`, `:1290`, `:1324`, `:1349`, `:1419`; deploy `:1358`, stop `:1379`, restart
`:1399`, logs `:1439`). No prototype chrome: no latency switch, no `track`/`wait` simulation.

**Blocked by: —**

**Status:** closed

- [x] Each prototype store method has a real implementation; loading and error state is exposed the way
      the prototype's screens expect to consume it
- [x] The API's nested shape (composes under an environment) is resolved as the monolith does today —
      no new endpoint is invented here
- [x] Method identity is stable across renders (the prototype hit an infinite-update loop with a
      per-render `useCallback`; do not reintroduce it)
- [x] Auth state drives the app entry: unauthenticated → setup/sign-in, authenticated → the shell
- [x] `pnpm --dir web typecheck` green; the data layer is covered by tests where it is a pure
      transform (window/state derivation), not by network mocks

## Comments

Implemented in this commit (`feat: add the API client and the fetch-backed panel store`).

- Split across three files: `data/api.ts` (one thin wrapper per endpoint, with the `error`-message
  → rejection mapping), `data/store.tsx` (the prototype's `PanelProvider`/`usePanel` shape), and the
  `data/types.ts` that ticket 02 already landed (extended with `findEnvironmentLocation`, the store's
  pure lookup beside `findComposeLocation`). The prototype's pending-key vocabulary is preserved
  exactly (`save-compose:3`, `deploy:3`, `status:3`, `bulk-variables:7`, …), verified against every
  `isPending(...)` call in the prototype's pages, so screens keep their loading wiring verbatim in
  06–09. New keys only where the prototype mocked things away: `auth-status`, `signin`, `signup`,
  `signout`, `projects`, `variables:<environmentId>`.
- Method identity is stable: every method is a `useCallback` over deps-free setters plus ref mirrors
  of the tree (`projectsRef`, `checkedAtRef`), so an effect depending on a method never refires when
  `pending` or data changes. `checkedAt` is stamped client-side after status-changing calls
  (GUIDELINES §11's "the prototype stamps it" option).
- Deliberate deviations, all API-shaped: `loadMetrics`/`loadServiceMetrics` are not ported — no
  endpoint exists and they are `.scratch/host-monitoring`'s seam (the spec lands that after 04/07);
  `activity`, `host`, `speed`/`setSpeed` and the `logs` cache are dropped (prototype chrome or
  design-only with no endpoint; the Compose page keeps its own logs state, as the prototype's screen
  already does); `Compose.lastAction` is gone (no screen reads it). `replaceVariables` composes a
  bulk replace from the existing per-variable calls (delete-missing, upsert-rest, re-read) since the
  API has no bulk endpoint. Auth: setup does not sign in — the entry flow moves on to sign-in, as the
  monolith does today; sign-out also drops the loaded tree.
- Backend behaviours surfaced by verification (recorded here since tickets may not touch Go): a
  created Project ships with a seeded `production` Environment (`environments.ProvisionDefault`), so
  `createProject` hydrates the new Project immediately instead of appending an empty list; and the
  API can report `environmentCount` 1 for a brand-new Project for the same reason.
- Verification: `pnpm --dir web typecheck`, `pnpm --dir web test` (19 pure tests, including the new
  `findEnvironmentLocation` — no network mocks in the committed suite) and `pnpm --dir web build`
  green. Additionally a throwaway end-to-end probe (run once against the real binary on a fresh
  `RELIST_DATA_DIR`, then deleted) exercised the whole client for real: setup → login → (+wrong
  password rejection) → Project/Environment/Variable/Compose CRUD → real `deploy` (docker compose
  up, status flips to running) → `logs` → `stop` (0 running) → cleanup → logout, with no leftover
  containers. The store's React layer is exercised from ticket 04 on, when the shell mounts the
  provider.
