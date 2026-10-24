# 07 — Refuse to merge a Deploy request into a queued one with different options

**What to build:** `Queue.Enqueue` returns the Compose's queued Deployment whatever the request
asked for, and the API answers 200 `{number}`. The UI then shows "Deployment #N queued" in both
cases. Requesting a plain deploy while a fresh-volumes one is queued therefore still wipes the
volumes; the reverse silently drops the requested wipe. Verified at the queue level.

**Blocked by:** none

**Status:** closed

- [ ] Same Mode/NoCache/FreshVolumes as the queued one: 200 `{number}` as today
- [ ] Different: 409 with the queued Deployment's number and options, e.g.
      "Deployment #4 is already queued with freshVolumes=true; cancel it first"
- [ ] The Compose page shows that message instead of "queued"
- [ ] Fix the comment on `enqueueDeployment` in `web/src/data/api.ts` ("200 when the same request
      is already queued")
- [ ] HTTP tests: fresh then plain → 409; plain then fresh → 409; same twice → 200

## Source

Stability review, item B2. Reproduction:
`.scratch/stability-review/verification/deployments_review_verify_test.go`.
