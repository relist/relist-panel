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

**Status:** closed

- [ ] Regular targets: a unique `.relist-write-*` temp beside the target — `os.Root` has no
      `CreateTemp`, so `OpenFile(tmp, O_CREATE|O_EXCL|O_WRONLY, 0666)` with a random suffix —
      `os.Root.Chmod` to an existing target's mode before the rename (a new file keeps the
      umask-derived mode), then `os.Root.Rename` over the target. A failure removes the temp and
      returns; the target is never opened, and a helper killed mid-run leaves a hidden temp, not a
      damaged original
- [ ] Symlink targets keep today's truncate-and-stream path: resolving symlink chains through
      `os.Root` is not worth it in v1, and the spec says the atomicity does not cover them
- [ ] The space guard's overflow clean-up moves into `volfs.Write` (delete the temp, never a
      pre-existing target); `runWrite`'s `vol.Delete(cmd.name)` goes away
- [ ] Tests: a replacing write that fails mid-stream leaves the original byte-identical; a
      successful write replaces it and keeps its mode; the free-space pre-check still refuses
      before any stdin is read; a symlink target still follows the link
- [ ] Delete the spec's Protocol and Caps sentences that describe the failure window when this
      lands

## Source

Second review of `7f42e5a...HEAD`, the "save fails and the original disappears" item (c3). Ticket
16's free-space accounting question is separate; this ticket owns the atomicity. Triaged
`ready-for-agent` after the third review; the spec documents the window until this lands.
