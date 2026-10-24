# 14 — Minor stalls (parked)

**What to build:** three small items, parked by the operator on 2026-10-24 until revisited.

1. A blocked maintenance guard (after an uncertain prune) makes Deploy answer a generic 500
   "could not queue the deployment" (verified) while the worker logs every second; map
   `ErrMaintenanceBlocked` to 503 with its message and log once per blocked period
2. Login runs bcrypt while holding the single DB connection (documented in `auth.go`); with
   `cpus: 0.25` every request waits during it
3. `.upload-*.zip` and `.src.new-*` left in a Compose directory by a crash mid-upload are never removed

**Blocked by:** none

**Status:** closed

## Source

Stability review, item C5.
