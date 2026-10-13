# Deployment safety implementation plan

**Goal:** Freeze queued execution inputs and make destructive/storage failures
visible and completion writes retryable.
**Spec:** `.scratch/deployment-safety/spec.md`, user approved in-chat design.
**Architecture:** Private persisted execution snapshot + immutable source revision;
existing warning records and shared Drawer; latest host metrics for sidebar.
**Tech stack:** Go/SQLite, React19/AntDesign6.5.1. No new external queue service.

- [x] Backend snapshot + Fresh volumes warning (worker1): composes capture/prepared
  deploy/build files, sources pinned Git/copy ZIP, deployment persistence migration
  and queue integration. TDD real DB/runner seams with post-enqueue edits and
  moving source, reuse stable deployment identities. Backend owns queue.go except
  completion retry handoff. Don't overwrite source work owned by another worker.
- [x] Completion retry primitive (worker2): create dedicated Go helper/tests in
  deployments; coordinate minimal integration into execute/settleUnstarted after
  snapshot owner finishes editing those blocks. Inject real SQLite failure, ensure
  retry holds the worker and Docker runs once; shutdown doesn't hang forever.
- [x] Frontend warnings + disk footer (worker3): existing modal clear deletion
  warning; footer via latest host metrics, threshold80%, correct unknown/stale
  behavior and Monitoring link. Token-driven responsive sidebar, meaningful tests.
- [x] Integration (root): browser fixtures desktop/mobile, independent backend/UI
  review, relevant Go race/tests/vet/build, full web tests/typecheck/frontend build.
  Update domain/UI documentation. Separate logical source and embedded commits.

## Constraints/review focus

Queue dedup must keep first snapshot/options; no process-global mutable input.
Do not hold a SQLite transaction through Git network/filesystem work. Source locks
and Project gate prevent snapshot races/deletes; network work has bounded context.
Snapshot credentials must not enter API/global list/log responses. Actual removal
warnings must never infer removal from freshVolumes alone. Build source artifacts
must survive restart and history pruning while live Containers can reference
them. Retain materialized trees until Compose teardown; discard canceled queued
trees and incomplete captures when they cannot have been mounted.

## Verification

- Full Go suite: `go test ./... -count=1` passed, including migration17 upgrade,
  actual API privacy checks, real Git/SQLite snapshots and completion recovery.
- Go race checks: complete deployments/composes/sources/dockerexec packages,
  relevant Deployment/Source/lifecycle HTTP cases and new migration/API privacy
  regressions passed. Full HTTP race run exceeded its 150s test budget in password
  hashing; the full HTTP normal suite passed, and focused HTTP race tests passed.
- `go vet ./...` and embedded panel binary build passed.
- Frontend: 543 tests across44 files, typecheck and production build passed.
- Headless Chrome fixtures: desktop1440px, mobile390px/320px; sticky disk footer,
  healthy/warning/stale/unavailable readings, mobile overlay, Fresh confirmation,
  persistent confirmed-removal warning, and no horizontal overflow passed.
- Independent review reproduced/fixed Source path variants using four read-only
  real Docker Compose/queue overlays. No live volume deletion or deployment ran.
- Existing Images cancellation bug reproduced on the pre-feature checkout and
  fixed separately: 20-repeat cancellation regression and full Docker adapter
  race suite passed. Cancellation now takes precedence over a simultaneous body
  EOF, retaining ordinary errors when the context remains active.

## Boundaries

New captures are private and persisted. Legacy queued rows capture current input
once and disclose the fallback; their original enqueue inputs cannot be recovered.
Git lookup is capped at20s, source copy at60s/4GiB/100000 files. Materialized source
trees remain until Compose teardown so pruning history cannot break live mounts.
Image tags and external host paths retain existing Docker behavior; final outcome
retries do not make Docker changes transactional or promise exactly-once execution.
