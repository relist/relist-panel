# 19 — Write atomically so a failed save cannot destroy the original

**What to build:** `volfs.Write` creates and truncates the target before it streams
(`os.Root.Create`), so a write that fails partway has already destroyed the original. This is
reachable over HTTP: the `Content-Length` requirement only enables the free-space pre-check, not
atomicity — a client abort mid-body surfaces as the stdin copy error on `exec.Wait` while the
helper saw EOF, wrote a partial file and exited 0, and an I/O error or the guard's clean-up leaves
the truncated target behind or removes it. The fix is to write a temporary file beside the target
and rename it over the target — same filesystem, atomic — so a failed write leaves the original
untouched.

**Blocked by:** none

**Status:** needs-triage

- [ ] Decide the semantics: a rename replaces a symlink rather than writing through it, so the
      current write-through behaviour and the spec's wording need an answer; the temp name must be
      collision-free and must not linger if the helper dies mid-run
- [ ] The space guard's overflow clean-up deletes the temp file, never a pre-existing target
- [ ] Tests: a replacing write that fails mid-stream leaves the original byte-identical; a
      successful write replaces it; the free-space pre-check still refuses before any stdin is read

## Source

Second review of `7f42e5a...HEAD`, the "save fails and the original disappears" item (c3). Ticket
16's free-space accounting question is separate; this ticket owns the atomicity.
