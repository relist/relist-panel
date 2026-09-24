# 03 — Data layer

**What to build:** `web/src/data/types.ts` + `web/src/data/store.tsx` with the prototype's shapes and
method names, but bodies that call the real API — the fetch call sites already exist in
`web/src/main.tsx` (auth status/setup/login/logout at `:109`, `:153`, `:174`; projects at `:394`,
`:433`, `:456`; environments at `:597`, `:648`, `:672`; variables at `:850`, `:899`, `:922`; composes
at `:1078`, `:1123`, `:1290`, `:1324`, `:1349`, `:1419`; deploy `:1358`, stop `:1379`, restart
`:1399`, logs `:1439`). No prototype chrome: no latency switch, no `track`/`wait` simulation.

**Blocked by: —**

**Status:** ready-for-agent

- [ ] Each prototype store method has a real implementation; loading and error state is exposed the way
      the prototype's screens expect to consume it
- [ ] The API's nested shape (composes under an environment) is resolved as the monolith does today —
      no new endpoint is invented here
- [ ] Method identity is stable across renders (the prototype hit an infinite-update loop with a
      per-render `useCallback`; do not reintroduce it)
- [ ] Auth state drives the app entry: unauthenticated → setup/sign-in, authenticated → the shell
- [ ] `pnpm --dir web typecheck` green; the data layer is covered by tests where it is a pure
      transform (window/state derivation), not by network mocks

## Comments
