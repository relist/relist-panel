# Relist Panel — UI redesign (design guideline)

A clickable, **mock-only** redesign of the panel's UI, built with the same stack the real frontend
uses (Vite + React 19 + TypeScript + antd 6 + Monaco). Every screen is reachable, every state is
deliberately rendered — and **nothing calls the API**.

This folder is the deliverable of a design pass. It is meant to be reviewed screen by screen, agreed
on, and then implemented in [`../web`](../web) as a separate piece of work.

```bash
cd web-design
pnpm install
pnpm dev          # http://localhost:5273
```

Other scripts: `pnpm typecheck`, `pnpm build:single` (bundles the prototype into one self-contained
`dist-single/index.html` that can be opened straight from disk), and `scripts/screenshot.sh`
(renders every route to PNG through headless Chrome — local review aid, not part of the design).

## What to look at

| Screen | Route | Notes |
| --- | --- | --- |
| Overview | `#/` | Host totals, "needs attention", activity, all Composes |
| Projects | `#/projects` | Cards **and** table view, filter, sort |
| Project | `#/projects/1` | Environment switcher + Composes / Variables / Settings |
| Environment Variables | `#/projects/1/env/1/variables` | Masked values, paste-a-`.env` bulk editor |
| Project settings | `#/projects/1/env/1/settings` | Rename, environment list, danger zone |
| Compose — running | `#/projects/1/env/1/compose/1` | Editor + status rail, logs drawer |
| Compose — partly running | `#/projects/1/env/1/compose/2` | `1/2` state, exited service, attention alert |
| Compose — never deployed | `#/projects/1/env/1/compose/3` | Dashed "Not deployed" tag, empty service list |
| Design guidelines | `#/design/guidelines` | The rules, rendered next to the screens they describe |
| States gallery | `#/design/states` | Loading, empty, error, feedback, destructive, narrow |
| Sign in / Setup | `#/signin`, `#/setup` | Split auth layout, two-step setup |
| Not found | `#/nope` | 404 with escape routes |

Two extras worth trying: **⌘K** (or the Search button) for the command palette, and the
**Mock latency** switch in the top strip — set it to *Slow* to watch every loading and pending state.

## Structure

```
src/
  app/            AppShell (sider + header), hash router, theme + design tokens
  components/     Reusable patterns: primitives, VariablesPanel, YamlEditor, DangerModal, CommandPalette
  data/           Domain types, fixtures (mocks.ts), in-memory store with simulated latency
  pages/          One file per screen
  monaco.ts       Monaco + compose-spec schema wiring (copied from ../web/src)
```

## Rules for this folder

- **No API calls.** Everything goes through `src/data/store.tsx`, whose method bodies are the
  stand-in for `fetch`. It is shaped so that swapping bodies for real requests does not change any
  component.
- **No new design vocabulary.** Colours, spacing, radii and motion come from antd tokens
  (`src/app/theme.ts`, `DESIGN.md` at the repo root). The only CSS file is `src/index.css`, which
  holds resets and a handful of interaction affordances.
- **This is not the product.** Do not import from `web-design` in `web`, and do not treat the hash
  router, the prototype strip or the latency switch as implementation decisions.

`GUIDELINES.md` in this folder is the written version of the design decisions; `#/design/guidelines`
is the same content rendered inside the prototype.
