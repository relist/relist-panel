# 01 — Theme and reset

**What to build:** Port `web-design/src/app/theme.ts` into `web/src/app/theme.ts`: the seed overrides
(`borderRadius`, `fontFamily`, `fontSize`, light-only `colorBgLayout`), the Layout / Menu / Table /
Card component tokens, the `LAYOUT` constants, the status vocabulary (`COMPOSE_STATE_META`) and
`CODE_FONT_STACK`. Wire it as `StyleProvider layer` + `ConfigProvider` (GUIDELINES §3). Replace
`web/src/index.css` with the design's reset and **remove tailwind** (deps, `postcss.config.cjs`,
`tailwind.config.js`, the `@layer tailwind-base, antd` ordering, and the monolith's utility classes —
those disappear with the monolith in 04, so this ticket only has to keep the old tree compiling).

**Blocked by: —**

**Status:** open

- [ ] `web/src/app/theme.ts` carries the same tokens as the prototype's, with the same `LAYOUT` keys
      (`siderWidth`, `barHeight`, `gutter`, `contentMaxWidth`, `editorMinHeight`) so screen tickets can
      copy markup across
- [ ] `ConfigProvider` wraps the app root with `buildTheme('light')`; `StyleProvider layer` keeps
      antd's rules in a layer so a reset cannot outrank them
- [ ] Monaco reads its colours from the same tokens (`monacoThemeData`) — the light theme only, since
      dark mode is out of scope (ADR-0004)
- [ ] tailwind is gone from `web/package.json`, the config files are deleted, and no file imports it
- [ ] `pnpm --dir web typecheck` and `pnpm --dir web build` are green, and the built app still renders
      the current screens with the new tokens

## Comments
