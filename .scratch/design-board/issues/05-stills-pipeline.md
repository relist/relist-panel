# 05 — `stills.mjs`: the capture pipeline

**What to build:** `web-design/scripts/stills.mjs` — Node 24, no new dependencies, CDP over the global
`WebSocket` — plus a `"stills"` script in `web-design/package.json`. It drives a **production build**
(`vite preview`, or the built `dist-single/`), never `pnpm dev`: `main.tsx` wraps the app in
`<StrictMode>` and React 19 double-invokes effects in development, so the mount-time `after` adapters
and `duration: 0` toasts would fire twice and `data-scene-ready` would flap.

**Pilot first.** The ticket's first deliverable is four frames — Overview and one Compose scene, in
light plus one overlay — not a hundred. Those four prove the mechanism, give the frame chrome something
real to sit on for 05, and produce the measurements 04 needs to report:

- bytes and milliseconds per frame for `format: 'webp'` at `quality: 90` and at default, versus PNG,
  for a 1600×1000 text screen. **CDP's WebP is lossy** — `quality` is an integer and there is no
  lossless switch — so the spec's "lossless, 8–12 MB" figure is a hypothesis until this exists.
- the sticky-header and overlay artefacts: `AppShell`'s header is sticky (`app/AppShell.tsx:221`) and
  antd's Drawer/Modal/message are fixed, so one drawer scene is captured and looked at before the
  remaining ~96 are taken.

Then the full run, per scene:

1. `Emulation.setDeviceMetricsOverride` at the scene's viewport (default 1600×1000), `deviceScaleFactor` 1.
2. Navigate to `?scene=<id>&theme=<mode>&clock=<fixed>#<hash>` — query before hash, since the hash is
   the router (`app/theme.ts:78` already reads `search` this way). The runner pins `TZ=UTC`, because the
   frozen clock makes a frame reproducible only once the timezone is fixed too.
3. Wait for readiness: `data-scene-ready` on `<html>` must carry **this scene's id** — a capture can
   never be of the wrong scene. This ticket extends the signal that 01 publishes with `pending` empty,
   `document.fonts.ready`, and Monaco mounted-and-idle for scenes with an editor.
4. Inject `animation: none !important; transition: none !important; caret-color: transparent` — a
   shimmer, a tooltip fade or Monaco's caret must not smear a frame.
5. Measure `scrollHeight` and `Page.captureScreenshot({ clip, captureBeyondViewport: true })`, so
   `clamp(640px, 80vh, 1000px)` (`theme.ts:37`) resolves against a genuine viewport while the image
   still shows the whole page. This is the content-height rule the spec changes deliberately; §4.9 of
   `GUIDELINES.md` records it (09). For a `device: 'mobile'` scene the viewport is 390×844 instead, and
   the same step applies — a phone frame is not a scaled desktop one.

Writes `public/stills/<id>.webp` **and** `src/scenes/manifest.json` — the manifest is a source
file, imported by the board rather than fetched, because `fetch()` from a `file://` page is blocked and
an offline review would otherwise render holes. One entry per still:
`{ id, device, theme, file, width, height, bytes, sha256, capturedAt, sourceRev }` (`theme` is always
`light`: there are no dark stills, per *Dark mode is a switch* — the field is kept so a dark pass later
is a scene field and a flag, not a migration), with `sourceRev` = `git rev-parse HEAD` plus a dirty flag.

Flags: `--only <id…>`, `--group <group>`, `--device desktop|mobile`, `--scale`, `--check`. `--check` re-captures into a
temp dir and compares hashes: same-machine, full-run cost, and it breaks on a Chrome or font update, so
it is documented as a milestone tool rather than a guard (there is no CI in this repo), with `sourceRev`
as the cheap staleness signal the board shows.

**Blocked by:** 01, 04

**Status:** needs-triage

- [ ] Pilot: four frames captured, looked at, and their bytes/ms recorded in this ticket's Comments —
      including the WebP-vs-PNG comparison and the resolution of the spec's `## Shipping the board`
      budget (commit, squeeze quality, or ship the set as an artifact). One of the four is a **mobile**
      scene, so the phone viewport is exercised before ~25 of them are queued
- [ ] The sticky header and one viewport-tall drawer are inspected in a pilot image and their behaviour
      written down, so the ~120 later captures are not a surprise
- [ ] Readiness is anchored to `?scene=`'s own id: a scene whose id does not come back never captures,
      and the runner fails loudly naming the scene
- [ ] Two captures of the same scene, a minute apart on the wall clock, are byte-identical (the
      determinism acceptance test the frozen clock exists for)
- [ ] A registry entry with no still does **not** fail silently: the run reports it, and the board (06)
      renders a labelled placeholder
- [ ] Every scene in the registry is either captured or listed in Comments as unrendered (the two
      fixtures 03 could not make reachable)
- [ ] `--only` / `--group` make a single-frame iteration a seconds-long loop, verified by capturing one
      scene twice while tuning the readiness wait
- [ ] Full run completes and its real wall-clock time replaces the spec's 10–15 minute estimate
- [ ] `pnpm --dir web-design typecheck` still green; no new dependency in `package.json`
