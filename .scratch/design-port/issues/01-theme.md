# 01 — Theme and reset

**What to build:** Port `web-design/src/app/theme.ts` into `web/src/app/theme.ts`: the seed overrides
(`borderRadius`, `fontFamily`, `fontSize`, light-only `colorBgLayout`), the Layout / Menu / Table /
Card component tokens, the `LAYOUT` constants, the status vocabulary (`COMPOSE_STATE_META`) and
`CODE_FONT_STACK`. Wire it as `StyleProvider layer` + `ConfigProvider` (GUIDELINES §3). Replace
`web/src/index.css` with the design's reset and **remove tailwind** (deps, `postcss.config.cjs`,
`tailwind.config.js`, the `@layer tailwind-base, antd` ordering, and the monolith's utility classes —
those disappear with the monolith in 04, so this ticket only has to keep the old tree compiling).

**Blocked by: —**

**Status:** closed

- [x] `web/src/app/theme.ts` carries the same tokens as the prototype's, with the same `LAYOUT` keys
      (`siderWidth`, `barHeight`, `gutter`, `contentMaxWidth`, `editorMinHeight`) so screen tickets can
      copy markup across
- [x] `ConfigProvider` wraps the app root with `buildTheme('light')`; `StyleProvider layer` keeps
      antd's rules in a layer so a reset cannot outrank them
- [x] Monaco reads its colours from the same tokens (`monacoThemeData`) — the light theme only, since
      dark mode is out of scope (ADR-0004)
- [x] tailwind is gone from `web/package.json`, the config files are deleted, and no file imports it
- [x] `pnpm --dir web typecheck` and `pnpm --dir web build` are green, and the built app still renders
      the current screens with the new tokens

## Comments

Implemented in this commit (`feat: port the design theme and drop tailwind`).

- `theme.ts` is a verbatim port of the prototype's file (seed, component tokens, `LAYOUT`,
  `COMPOSE_STATE_META`, `SERVICE_STATE_META`, `CODE_FONT_STACK`, `ThemeModeContext`,
  `monacoThemeData`), so later tickets can copy markup across without drift. The dark branches stay
  typed but nothing wires them: `main.tsx` calls `buildTheme('light')` and `monaco.ts` registers only
  `MONACO_THEME.light` (dark mode is out of scope, ADR-0004). The monolith's two `<Editor theme="vs">`
  now use `MONACO_THEME.light` so the editor visibly wears the tokens.
- `index.css` is the prototype's reset wholesale; its `.rp-*` helpers are unused until the shell lands
  in 04. Tailwind is fully removed: `tailwindcss`/`postcss`/`autoprefixer` dropped from
  `web/package.json`, `postcss.config.cjs` and `tailwind.config.js` deleted, no `@tailwind`/`@layer`
  directives left. The monolith's utility class names remain as inert strings (deleted with the
  monolith in 04), so spacing on the old screens is loose until then — expected per the ticket.
- Self-review findings, fixed: the first cut dropped antd's `theme` import together with the
  algorithm swap, but three monolith components still call `theme.useToken()` — import restored.
- Verification: `pnpm --dir web typecheck` and `pnpm --dir web build` green; `go build` green and the
  binary serves `/` (200) and `/api/auth/status` on a fresh `RELIST_DATA_DIR`. This environment's
  headless Chrome cannot reach any local `http://` URL (hangs even on a static file server), so the
  "renders with the new tokens" check ran the built bundle through an offline single-file render
  (bundle + reset inlined, `/api/*` answered by a fetch shim) driven via `file://`: the Projects
  screen (authenticated fixture) and the setup screen (unauthenticated fixture) both paint with the
  new sider/menu/card/primary-button tokens. Rebuilt embedded `dist/` included per repo convention.
