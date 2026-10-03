# 18 — Module-shape follow-ups from the volume review

**What to build:** the review's judgement-call smells — none of them bugs. Triage each item: do it,
or record an explicit won't-fix and close.

**Blocked by:** none

**Status:** needs-triage

- [ ] The "uses" renderer is duplicated between `ComposeVolumesTab.tsx` and
      `VolumeExplorerPage.tsx`
- [ ] `"volume "+op+" failed: "` repeats in `internal/volumeapi` — one helper would do
- [ ] `(location, source, path, access)` travels as a clump through `web/src/data/api.ts`
- [ ] `volumeapi.Service` clashes with the glossary's Service; `volfs`/`volumeapi` are
      abbreviations where ADR-0003 asks names to mirror `CONTEXT.md`
- [ ] `VolumeExplorerPage.tsx` is 1,315 lines and owns the card, listing, toolbar, editor and
      dialogs
- [ ] `TextFileEditor` is used only by `VolumeFileEditor`
- [ ] `volumes.RefusalError` / `ArchiveRefusedError` / `ExistsError` are structurally identical — a
      `Message` and an `Error()`; keep three types (each drives a distinct HTTP status) or collapse
      them into one with a kind, and record the decision either way

## Source

Review of `7f42e5a...HEAD`, "Code smells". The exit-code "bare ints" item was wrong — both the
helper (`exitRefused`/`exitArchiveRefused`/`exitExists`) and `dockerexec`
(`helperRefusedExitCode`/`helperArchiveRefusedExitCode`/`helperExistsExitCode`) name every code.

## Comments

**Second review:** the redundant user validation — `operationUser` re-checking the pattern
`numericUser` had checked, and `operationOf` re-parsing the uid — was removed this round
(`numericUser` now returns the parsed uid and `operationUser` passes it through). The three
structurally identical error types were the review's untracked item and are now listed above.
