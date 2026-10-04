# Tailwind is gone, and routing is react-router

The redesign port (`.scratch/design-port/spec.md`, tickets 01–04) removed Tailwind from `web/`
entirely and adopted react-router. Both choices were settled in the spec on 2026-09-24 but landed
without an ADR; this records them, in ADR-0006's manner of noting departures without editing the
ADRs they depart from.

- **Tailwind is dropped.** ADR-0001 fixes the stack as "Vite + React + TypeScript + Tailwind" and
  ADR-0004 devotes a section to coexisting with it; the redesign makes both stale. The ~30 uses in
  the old monolith were spacing utilities the design expresses with antd `Flex`/`Space` and the
  `LAYOUT` constants, and no design screen uses Tailwind. Styling now comes from antd's
  `ConfigProvider`/`buildTheme('light')` with `StyleProvider layer` (so a reset can never outrank
  antd's rules) plus the design's reset in `web/src/index.css`. The tailwind/postcss/autoprefixer
  dependencies, `postcss.config.cjs` and `tailwind.config.js` are deleted.

- **Routing is react-router**, with the design's paths as history URLs
  (`/projects/1/env/1/compose/1`). GUIDELINES §12 leaves the library choice open; the prototype's
  hash router was a review artifact and was not ported. History URLs required the two small Go
  changes ticket 04 records: `webassets` serves the shell for any path that is not a real file, and
  `auth.publicPath` treats anything outside `/api/` (the bare `/api` included) as public.

Consequences: there is exactly one styling system — a screen that reaches for utility classes has
nowhere to import them from, and component tokens in `web/src/app/theme.ts` are the single place
styling vocabulary lives. URL shape is now user-visible and linkable, which the tab-in-path
conventions (`/projects/:p/env/:e/:tab`, `…/compose/:c/:tab`) and the `?window=` metrics control
build on.
