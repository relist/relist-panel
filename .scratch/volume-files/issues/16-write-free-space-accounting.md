# 16 — Account for the replaced file in write's free-space check

**What to build:** `write` measures free space before it opens the target, so an edit that would
fit after the old file's blocks are freed can still be refused with 507, and the stream guard's
budget uses the same pre-truncation number. The review's data-loss scenario — the guard deleting
the original on overflow — is only reachable by invoking the helper directly: over HTTP the body is
bounded by `Content-Length`, and ticket 08 documents the truncate-and-remove trade-off.

**Blocked by:** none

**Status:** needs-triage

- [ ] Decide: stat the target first and add its size to the pre-check and the guard budget (which
      then needs an answer for deleting a pre-existing target on overflow), or keep the
      conservative check and say so in the spec
- [ ] Whatever lands, helper tests cover a replacing write whose target is larger than the free
      space
- [ ] The spec's Caps section says "refused before anything is read" — a stat does not read bytes

## Source

Review of `7f42e5a...HEAD`, "Implemented but wrong" #3; ticket 08's comment explains why the write
truncates and removes its partial file.
