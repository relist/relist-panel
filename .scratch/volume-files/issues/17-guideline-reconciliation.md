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
      indent) are sizes, not gaps; tokenise or exempt them explicitly

## Source

Review of `7f42e5a...HEAD`, "Hard violation 2" and "Likely violation: a small control outside a
table". The rule as written is stricter than the codebase has ever been.
