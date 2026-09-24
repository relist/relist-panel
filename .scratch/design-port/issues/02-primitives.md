# 02 — Primitives and the frontend test setup

**What to build:** Port the prototype's shared components into `web/src/components/`:
`primitives.tsx` (`PageHeader`, `StatCard`, `EmptyState`, `CodeValue`, `RelativeTime`,
`ComposeStatusTag`, `ComposeStatusInline`, `ServiceStateTag`, `HealthBadge`) and `DangerModal.tsx`
(type-the-name confirmation listing what is destroyed). Add vitest with a `test` script and cover the
pure helpers this port introduces.

**Blocked by: —**

**Status:** ready-for-agent

- [ ] Every component matches the prototype's props and markup, including `PageHeader`'s
      title/tags/description/right-actions/hairline contract (GUIDELINES §6)
- [ ] `DangerModal` requires the object's name typed exactly and lists what disappears
- [ ] vitest is a devDependency with a `test` script; `pnpm --dir web test` runs
- [ ] Pure helpers have tests (unit formatting, relative time, status derivation) — no component tests
- [ ] The old tree still compiles: components are additive until the screens switch in 06–09

## Comments
