# 17 — Reconcile the spacing and control-size rules with the codebase

**What to build:** `docs/GUIDELINES.md` §6 says gaps come from 8/16/24 and §7 says only in-table
actions drop to `size="small"`, but the panel uses 4/6/12 gaps and `size="small"` in 39 places
(alerts' actions, the Explorer's up-crumb, auth links), including files outside the reviewed range.

**Blocked by:** none

**Status:** needs-triage

- [ ] Decide the real scale — antd's 4/8/12/16/24 is the obvious candidate — and amend §6/§7, or
      keep 8/16/24 and sweep every screen. Half-fixing only the newest files would make the
      codebase less consistent, not more
- [ ] While there: the arbitrary values the review flagged (`48px` spinner padding, `20px` list
      indent) are sizes, not gaps; tokenise or exempt them explicitly. The `20px` indent is now
      `LAYOUT.gutter`; the `48px` loading-placeholder inset stays in `VolumeExplorerPage.tsx` and
      three older files (`VariablesPanel`, `ComposePage`, `ProjectPage`), so the decision covers all
      four
- [ ] `GUIDELINES.md` §6's Volume Explorer paragraph references `LAYOUT.volumeEditorHeight`
      (`clamp(320px, 45vh, 560px)`), which does not exist — the shipped editor is the full-screen
      modal in `VolumeFileEditor.tsx`; rewrite the paragraph to match

## Source

Review of `7f42e5a...HEAD`, "Hard violation 2" and "Likely violation: a small control outside a
table". The rule as written is stricter than the codebase has ever been.

## Comments

**Third review:** the two new volume screens are now fully on the 8/16/24 scale — gaps, margins,
the up-crumb, the alert action and the literal `16` all tokenised or on-scale. What remains for
this ticket is the codebase-wide sweep and the two items above.
