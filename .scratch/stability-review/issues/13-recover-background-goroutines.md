# 13 — Recover panics in background goroutines

**What to build:** a panic in the sampler loop, the container sweep, the docker df refresh,
`ResumeLock` or the migration target goroutines kills the whole panel (verified: a panicking
`SweepContainers` hook takes the test binary down). Only the deployment worker recovers today.

**Blocked by:** none

**Status:** closed

- [ ] A small helper that runs a function, recovers, and logs the panic with its stack; used by the
      goroutines above. A recovered sampler hook skips that round and the next tick runs normally
- [ ] Test: a panicking `SweepContainers` hook is logged and the following tick still records a sample

## Source

Stability review, item A4. Reproduction:
`.scratch/stability-review/verification/hostmetrics_review_verify_test.go`.
