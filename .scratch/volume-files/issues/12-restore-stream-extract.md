# 12 — Restore: streamed tar.gz extraction

**What to build:** Restoring an uploaded tar.gz into a directory that already exists, streamed end
to end with nothing staged on the panel's disk. Upload and extract are one operation; extraction is
the sharpest edge in the feature, so this lands last.

**Blocked by:** 02, 03, 13, 05

**Status:** ready-for-agent

- [ ] `internal/volfs` gains `extract`: every entry is validated **before it is written** — names relative and free of `..` segments, no absolute paths, symlink and hardlink entries refused outright — and a refusal aborts the rest of the stream
- [ ] The helper gains `extract -- <relpath>`, consuming the tar.gz on stdin; the target is a directory relative to `/data` that must already exist, directories inside the archive are created, existing files are overwritten and directories merge
- [ ] One HTTP endpoint streams the request body straight into the run's stdin with the Volume mounted `:rw` — upload and extract are one operation, and nothing is staged on the panel's disk
- [ ] Caps enforced end to end: compressed ≤ 1 GiB on the request body, uncompressed ≤ 4 GiB and ≤ 10k entries counted by the helper as it streams
- [ ] The UI discloses, before the operator runs it, that a stream that fails mid-way leaves the entries already written in place — extraction is partial, there is no transaction across a stream — and names the target directory
- [ ] Tests against malicious archives: `..` entries, absolute entries, symlink and hardlink entries, and the entry-count and uncompressed-size caps
