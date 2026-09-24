# 04 — Router and shell

**What to build:** History routing with the design's paths, and the shell around every screen. Add
react-router; `AppShell` (248px sider with sections and recent Projects, 56px sticky header with the
right-hand cluster pushed by `marginInlineStart: auto`, breadcrumb, attention badge, host footer);
`Layout.Content` = 24px-padded, 1280px-max column. On the Go side: `webassets` serves the shell for a
path that is not a real file, and `auth.publicPath` treats anything outside `/api/` as public.

**Blocked by: 01, 02, 03**

**Status:** closed

- [x] `#/` → `/`, `#/projects` → `/projects`, `#/projects/:p`, `#/projects/:p/env/:e/:tab`,
      `#/projects/:p/env/:e/compose/:c`, `#/projects/:p/env/:e/compose/:c/:tab` all resolve, and an
      unknown path renders the design's not-found rather than a blank page
- [x] Deep links work: a fresh load of `/projects/1/env/1/compose/1` (unauthenticated *and*
      authenticated) reaches the app, not a 404 or a 401 page
- [x] `/api/*` is still authenticated; `/assets/*` is still served; `/healthz` still public
- [x] `go test ./...` green, with an HTTP-seam test for the fallback (unknown non-API path → 200
      `index.html`; unknown `/api/` path → 401/404 as before)
- [x] The shell's header cluster lands on the 24px gutter on every route, including routes with a
      single crumb (the prototype's own bug — GUIDELINES §6)
- [x] The design docs' `#/…` links are swept to the new form (`web-design/GUIDELINES.md` and the
      in-app guidelines page, which stays a prototype)

## Comments

Implemented in this commit (`feat: cut over to the routed app shell`).

`react-router` (v8.4.0) added as the spec's one mandated dependency; routes
live in `app/routes.tsx` over the grammar in `app/paths.ts` (`paths` builders + `routePaths` patterns
+ `resolvePath`, the ticket's pure vitest seam). The cutover replaced `web/src/main.tsx` with the new
root — theme, `PanelProvider`, an auth gate, then `RouterProvider` — and deleted the monolith screen
tree (~1,400 lines). Screens for tickets 05–09 do not exist yet, so their routes resolve to minimal
placeholder pages built from `PageHeader`/`EmptyState` (`components/TicketPlaceholder.tsx`); each
page names the ticket that replaces it.

Verified: `pnpm --dir web typecheck` and `test` (24 tests, 5 new on `resolvePath`/`paths`), `build`,
`go vet`, gofmt, and `go test ./...` with the new seam test `TestDeepLinksServeTheAppShell` (unknown
non-API path → 200 `index.html` unauthenticated and authenticated; unknown `/api/` path → 401/404 as
before; a `/assets/…` file discovered from the served shell still 200s). Also curl'd against the
built binary (`/healthz` 200; deep link 200 `text/html` signed out and in; `/api/unknown` 401 → 404)
and rendered through the file:// screenshot technique: `/` (single crumb — no breadcrumb, cluster on
the gutter), the compose deep link (five-crumb trail "Overview / Projects / my-saas / production /
web"), `/nope` (the design's not-found inside the shell), and the unauthenticated gate.

Deviations, all deliberate:

- Two dependencies, not one: the shell's markup ports from the prototype, which imports
  `@ant-design/icons` directly (`6.3.4`, pinned to the prototype's version); pnpm's strict layout
  does not expose antd's transitive copy.
- `routes.tsx` passes `basename` (`import.meta.env.BASE_URL`, trailing slash dropped) to
  `createBrowserRouter` — a no-op at `/` that also lets the built bundle render under a sub-path, and
  what makes the file:// render check exact.
- Omitted from the prototype shell per the spec's omissions list: the prototype strip, the latency
  switch, the ⌘K Search button + `CommandPalette`, and the dark-mode switch (ADR-0004 defers it).
  The right cluster is therefore refresh-with-attention-badge + account menu. The sider's host footer
  shows "Docker host" — the prototype's `Docker x · Traefik y` readout has no API source (§11), and
  no host name is invented.
- The prototype's sider menu scrolled in its own container (`.rp-sider-scroll`); the port drops that
  wrapper so the page scrolls as one document (no nested scrollers). The sticky header is unchanged.
- The gate replaces the monolith's setup/login screens until ticket 05: while
  `!auth.authenticated` it renders a placeholder built from `EmptyState`; a failed
  `/api/auth/status` read surfaces the monolith's Retry card instead of spinning forever. Sign-in
  state is what gates the app, not the path, so a deep link survives signing in. The account menu's
  Sign out is real (`store.signOut`).
- Doc sweep nuance: app routes (`/`, `/projects`, `/signin`, …) are swept to history URLs in
  `GUIDELINES.md` and the in-app guidelines page; the two design-system screens keep `#/design/…`
  because they remain prototype-only.
- The prototype AppShell source already carried the `marginInlineStart: 'auto'` fix for the
  single-crumb cluster; this ticket ports it and proves it per route class with screenshots.
