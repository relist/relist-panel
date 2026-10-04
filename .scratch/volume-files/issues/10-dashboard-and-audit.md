# 10 — Dashboard banner and audit rows

**What to build:** The visibility into handles left open, and the record of what was done to a
Volume. The live view is a banner; the history is rows with no screen yet.

**Blocked by:** 01, 04, 08

**Status:** wontfix

- [ ] The Overview shows a banner listing every live handle with its Volume, its Compose, and the idle countdown, each with a Stop control
- [ ] The Volumes tab shows a badge when that Compose has a live handle
- [ ] The banner is about exposure as much as resources: a live handle is a container holding a Volume, which exists because someone opened it and did not close it
- [ ] A migration adds an append-only events table; start, stop, write, delete and archive each append a row recording who, when, handle, path and bytes
- [ ] No viewer screen in v1, deliberately: rows not written now can never be reconstructed, whereas a screen over rows already held is a later addition with no migration
- [ ] HTTP-seam tests asserting one row per operation, written on success and not on failure

## Comments

Closed `wontfix` by the re-plan: there is no dashboard in this model — nothing stands
between operations, so there is nothing to show — and the audit rows are deferred
entirely. Single admin, no viewer screen yet; recording them later is additive.
