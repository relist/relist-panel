# 09 — Volume reads must not block Deployments

**What to build:** GET requests under `/volumes` hold the Project gate for the whole request
(`internal/httpapi/coordination.go`), so an archive download of hours keeps that Project's Deploy
step waiting (up to the 60-minute timeout), and because the host queue runs one Deployment at a
time, every other Project's Deployment waits behind it. Verified through the HTTP handler.
Decision (2026-10-24): split the gate into read and write.

**Blocked by:** none

**Status:** closed

- [ ] Volume list/stat/read/archive take a shared (read) hold; Compose/Project/Environment delete,
      teardown, volume writes and fresh-volumes deploys take the exclusive hold
- [ ] A normal Deploy step does not wait for read holds
- [ ] Waiting for the exclusive hold still returns promptly on context cancel, and the host
      maintenance guard keeps working as today
- [ ] Turn the C2 reproduction into a test: with an archive open in Project A, a Deployment of A and
      one of Project B both succeed; a Delete of A waits for the archive

## Source

Stability review, item C2. Reproduction:
`.scratch/stability-review/verification/httpapi_review_verify_test.go`.
