# 09 — Ship it, and retire what it replaces

**What to build:** The last mile — the offline path, the removals, the documentation, and the decision
record. Nothing here is hard; all of it is easy to forget, which is why it is a ticket rather than a
trailing paragraph.

**The offline path, verified rather than assumed.** `pnpm --dir web-design build:single`, then open
`dist-single/index.html#/design/board` **from disk**, and the board must render every frame. The three
things that make that true are in `## Shipping the board` and each is checked here:

- the manifest is imported from `src/scenes/manifest.json` (a `fetch()` of it works in dev and fails on
  `file://`, so dev passing proves nothing);
- stills are referenced relatively from `public/stills/**`, which Vite copies into `dist-single/`, and
  are **not** `import.meta.glob`-ed into the bundle (`assetsInlineLimit: 100_000_000` would base64 the
  set);
- `dist-single/` stays gitignored — the folder is handed over, not committed. Record its total size in
  Comments so the handover is a known quantity.

**Retire `#/design/screens`**: the route, `pages/ScreensGalleryPage.tsx`, `paths.screens`, the route kind
in `router.ts`, `main.tsx`'s `screensGallery` branch and its `bare` entry, and the page's comment header.

**Keep `#/design/states` as a scene host**: retitle it so it no longer claims to be the states gallery,
delete its volume-specific sections (duplicates of real screens), and leave `paths.states`,
`GuidelinesPage.tsx:586`'s link (reworded) and `scripts/screenshot.sh`'s route line alone — that script's
`states` capture is the check that the route still resolves.

**Documentation:**
- `web-design/README.md` — `#/design/board` becomes the review surface, the two long paragraphs about
  live frames and 18 screens are replaced with what a still board is and is not, plus the offline path,
  the stills' size, one sentence that the stills are light (dark is reviewed through the switch into the
  live mock), and the mobile frames.
- `GUIDELINES.md` §4.9 — the design-system section describes the board; it also records the
  content-height-frame rule the board changes (the gallery's own header argues the opposite for its own
  frames, so the two files must stop contradicting each other).
- `GUIDELINES.md` §6 — the four descriptive responsive sentences are replaced by the designed mobile
  contract from 03: the 992px breakpoint, what changes under it per pattern (nav as a drawer, stacked
  rows instead of tables, sheets instead of dialogs, scrolling tab bars, hit targets), and the rule that
  the board is desktop-only.
- `docs/adr/` — the ADR `## Shipping the board` calls for: the review surface is stills committed to the
  repo, with the measured sizes, the lossy-WebP caveat and the rejected alternatives (gitignored stills,
  live iframes).
- `.scratch/volume-files/issues/01-design-pass.md` — its review surface points at the board instead of
  `#/design/screens`. The review gate itself does not move: the volume implementation still waits for
  approval.

**Blocked by:** 06, 07, 08

**Status:** needs-triage

- [ ] A clean `pnpm --dir web-design build` and `build:single` both succeed, and the board renders from
      `dist-single/index.html` opened off disk with no dev server and no network — mobile frames included,
      since a phone rail that only resolves on the dev server is exactly the failure this ticket exists to
      prevent
- [ ] The offline board is checked on a **fresh clone** (or after `git clean -xdf` + `pnpm install`),
      because "it renders here" with a warm `node_modules` and an old `dist-single/` is not the check
- [ ] The committed stills' total size is recorded in Comments and is inside the budget the spec's
      `## Shipping the board` sets; if it is not, the fallback named there is applied instead of shipping
      an oversized set
- [ ] `#/design/screens` and everything that names it is gone — `grep -rn 'design/screens\|screensGallery\|ScreensGalleryPage' web-design/` returns nothing
- [ ] `#/design/states` still resolves (captured by `scripts/screenshot.sh`), is no longer linked as the
      states gallery, and its volume-specific sections are gone
- [ ] README, GUIDELINES §4.9 and §6, and the ADR describe the board and the mobile contract, and
      `GUIDELINES.md` no longer contradicts the board's frame rule or leaves the responsive story as four
      descriptive sentences
- [ ] `volume-files` ticket 01's review surface points at the board, and its review gate is still open
- [ ] `pnpm --dir web-design typecheck` and `node --test scripts/*.test.mjs` green; the whole story is
      one commit per concern (docs separate from removals separate from the shipping config)
