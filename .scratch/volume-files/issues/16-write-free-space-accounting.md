# 16 — Account for the replaced file in write's free-space check

**What to build:** `write` measures free space before it opens the target, so an edit that would
fit after the old file's blocks are freed can still be refused with 507, and the stream guard's
budget uses the same pre-truncation number. The review's data-loss scenario is real but belongs to
ticket 19: the truncate-then-stream write can lose the original on a client abort or an I/O error
even with `Content-Length` present, and the fix there is atomicity, not accounting. This ticket is
only the conservative check.

**Blocked by:** none

**Status:** wontfix

- [ ] Decide: stat the target first and add its size to the pre-check and the guard budget (which
      then needs an answer for deleting a pre-existing target on overflow), or keep the
      conservative check and say so in the spec
- [ ] Whatever lands, helper tests cover a replacing write whose target is larger than the free
      space
- [ ] The spec's Caps section says "refused before anything is read" — a stat does not read bytes

## Source

Review of `7f42e5a...HEAD`, "Implemented but wrong" #3; ticket 08's comment explains why the write
truncates and removes its partial file.

## Comments

**Triage (second review):** `wontfix`. The conservative check is kept and now stated in the spec's
Caps: the replaced file's own blocks are not counted, so an edit that would fit after they are freed
can still answer 507 — safe, and rare on the volumes this feature is sized for. The data-loss path
the review flagged is tracked in 19, where the fix (write a temp file and rename) also settles what
an overflow should delete.
