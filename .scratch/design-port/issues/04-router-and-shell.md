# 04 — Router and shell

**What to build:** History routing with the design's paths, and the shell around every screen. Add
react-router; `AppShell` (248px sider with sections and recent Projects, 56px sticky header with the
right-hand cluster pushed by `marginInlineStart: auto`, breadcrumb, attention badge, host footer);
`Layout.Content` = 24px-padded, 1280px-max column. On the Go side: `webassets` serves the shell for a
path that is not a real file, and `auth.publicPath` treats anything outside `/api/` as public.

**Blocked by: 01, 02, 03**

**Status:** open

- [ ] `#/` → `/`, `#/projects` → `/projects`, `#/projects/:p`, `#/projects/:p/env/:e/:tab`,
      `#/projects/:p/env/:e/compose/:c`, `#/projects/:p/env/:e/compose/:c/:tab` all resolve, and an
      unknown path renders the design's not-found rather than a blank page
- [ ] Deep links work: a fresh load of `/projects/1/env/1/compose/1` (unauthenticated *and*
      authenticated) reaches the app, not a 404 or a 401 page
- [ ] `/api/*` is still authenticated; `/assets/*` is still served; `/healthz` still public
- [ ] `go test ./...` green, with an HTTP-seam test for the fallback (unknown non-API path → 200
      `index.html`; unknown `/api/` path → 401/404 as before)
- [ ] The shell's header cluster lands on the 24px gutter on every route, including routes with a
      single crumb (the prototype's own bug — GUIDELINES §6)
- [ ] The design docs' `#/…` links are swept to the new form (`web-design/GUIDELINES.md` and the
      in-app guidelines page, which stays a prototype)

## Comments
