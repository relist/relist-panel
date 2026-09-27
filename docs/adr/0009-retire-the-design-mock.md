# The design mock is retired; the guidelines move to `docs/`

The redesign was reviewed through an API-free, clickable mock in `web-design/` (its contract was
`GUIDELINES.md`). The port into `web/` closed on 2026-09-27 (`.scratch/design-port/`, 10/10 tickets),
and the mock's last open design question — the responsive pass (ticket 03) — was closed `wontfix`
the same day. The mock is deleted in that pass (`.scratch/design-board/issues/14-retire-the-mock.md`).

- **The mock leaves the working tree, but not the repository.** Nothing imports it: no build, no
  test and no runtime path in `web/` or the Go side reads it, so deleting it costs the panel
  nothing. Its archive is the annotated tag `design-mock`, on the last commit that carried it. The
  mock's own review surfaces — the still board, the task flows, the scene map, the states gallery —
  go with it: they answered how to read a design under review, not how the panel works.

- **The design contract survives outside the mock.** `GUIDELINES.md` moves to
  `docs/GUIDELINES.md` with its **section numbers unchanged**, because `web/src/` cites it by
  section in 17 files and renumbering would break every one of them. The registry's 105 states are
  rendered into `docs/design-states.md` — id, route, and the `ui:`/`after:` knobs that force each
  state — including the states the port deliberately omitted (dark mode, the ⌘K palette, the
  Activity cards), which is what makes it a checklist rather than a changelog.

- **Retired scaffolding is a tag, not a folder.** The mock's source already lives in this
  repository's history; the tag only names the point where it was whole, so a future pass (mobile,
  dark mode) checks it out and reads the design instead of rebuilding it.

Consequences: `docs/` is the home of the design contract (`GUIDELINES.md`, `design-states.md`)
beside the ADRs, and no folder in the tree holds a second product. A reviewer looking for the
clickable mock follows the tag; a new design pass starts from it, not from a revived `web-design/`.
The phase's tickets stay in `.scratch/design-board/` as the record of how the mock was built and
what its review surfaces were for.
