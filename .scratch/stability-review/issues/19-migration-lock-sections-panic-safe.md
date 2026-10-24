# 19 — Migration target state stays usable after a recovered panic

**What to build:** `internal/migration/target.go` takes `t.mu` in many short sections without a deferred unlock. Since ticket 13 a panic in the run goroutine is recovered and its error path takes `t.mu` again, so a panic inside one of those sections would deadlock the migration and its status endpoint. Route every such section through a helper that takes the lock and defers the unlock, so a panic always releases it.

**Blocked by:** none

**Status:** ready-for-agent

## Source

Parked during the stability-review implementation (Task 14), approved by the operator 2026-10-25.
