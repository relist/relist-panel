# 12 — Bound the memory a Volume listing can take

**What to build:** `volfs.List` reads the whole directory and the panel buffers the helper's JSON
in a `bytes.Buffer` before answering. Measured: 138 B per entry; one million entries peaks around
192 MB of heap in the panel, against a 256M container limit with no `GOMEMLIMIT`. An OOM kill
restarts the panel and fails a running Deployment as "panel restarted".

**Blocked by:** none

**Status:** closed

- [ ] The helper's `list` stops at a cap (e.g. 10,000 entries, sorted by name) and returns
      `{"entries": [...], "truncated": true}`; the Explorer says only the first N are shown
- [ ] `GOMEMLIMIT: 200MiB` on the panel service in `docker-compose.yml`
- [ ] A `--memory` limit on the helper's `docker run`
- [ ] Tests: helper list truncates with the flag; the HTTP layer passes `truncated` through

## Source

Stability review, item A2. Measurement:
`.scratch/stability-review/verification/volfs_review_verify_test.go`.
