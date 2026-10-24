# 18 — Boot snapshot collection does not delay the Deployment queue

**What to build:** `Queue.Run` collects every Compose's snapshots before the worker takes its first Deployment, with up to two docker calls of 30 s each per Compose. A hung daemon delays every deploy after boot. Run the boot collection in the background once the worker has started (each collection already takes the Project gate and volume hold), inside `safely.Do`, stopping when the queue's context ends, and waited for on shutdown.

**Blocked by:** none

**Status:** ready-for-agent

## Source

Parked during the stability-review implementation (Task 9), approved by the operator 2026-10-25.
