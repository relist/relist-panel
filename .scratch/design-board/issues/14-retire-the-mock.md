# 14 — Retire the mock: harvest the guidelines and the state list, tag, delete

> **Decided — 2026-09-27.** The port into `web/` closed (`.scratch/design-port/`, 10/10 tickets) and
> the mock stopped having a job: nothing imports it, no build reads it, and its last open question
> — 03's mobile pass — was closed `wontfix` on this ticket's authority. What was left is a second
> product (engine, tests, four review surfaces) answering questions nobody is asking any more, at
> 331 MB on disk. So the mock leaves the working tree, after its two living parts are harvested.

**What was harvested, and why each one is live:**

- **`docs/GUIDELINES.md`** (moved from `web-design/`) — `web/src/` cites this file by section in 17
  files (`§4.1`, `§11`, `§12`). Section numbers are unchanged so every citation still resolves; the
  header now records where the mock went, and §4.9/§12 — the mock's own surfaces and the port order
  — are marked as history.
- **`docs/design-states.md`** — the registry's 105 states rendered as a checklist: id, title, route,
  and the `ui:`/`after:` knobs that force it. The registry was the mock's most durable artefact
  (every state the design defined, including the ones the port deliberately omitted — dark mode,
  the ⌘K palette, the Activity cards), and deleting the mock would have taken it with it.
- **One citation** — `web/src/data/store.tsx`'s "the prototype's store shape" now names the tag
  instead of the deleted path.

**What was not kept:** the running mock itself. Its archive is the annotated tag **`design-mock`**
on the commit before the deletion, so `git worktree add /tmp/design-mock design-mock` restores the
whole apparatus — registry, board, flows, map, tests — and `docs/design-states.md`'s header says so.
`dist-single/index.html` was not committed (5.7 MB, and reproducible from the tag with
`pnpm build:single`). `.scratch/design-board/` stays as the phase's own record, pointing at the tag.

**Status:** ready-for-human

- [ ] `docs/GUIDELINES.md` exists in `docs/`, section numbers unchanged, and every `§` citation in
      `web/` still resolves
- [ ] `docs/design-states.md` carries all 105 states with their captions and forcing knobs
- [ ] `design-mock` tag restores `web-design/` intact (`git worktree add … design-mock`)
- [ ] `web-design/` is gone from the working tree; the only remaining mentions are the retirement
      record itself (`docs/GUIDELINES.md`, `docs/adr/0009`, `docs/design-states.md`) and `.scratch/`
- [ ] `pnpm --dir web typecheck` and `pnpm --dir web test` green
- [ ] `.scratch/design-board/issues/03-responsive-mobile.md` is `wontfix` with the decision recorded

- 2026-09-27 — **Landed.** Harvest `e5614fe` (the state list) and `679a59d` (the guidelines move and
  ADR-0009); deletion `622b7c7`. Tag `design-mock` sits on `9aec0ca`, the last commit that carried
  `web-design/` whole — 83 files, verified with `git ls-tree`. `docs/design-states.md` holds 105
  states in 9 groups; `pnpm --dir web typecheck` and `pnpm --dir web test` (136 tests) were green
  after the harvest, before anything was deleted.
