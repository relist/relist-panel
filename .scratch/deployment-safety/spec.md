# Deployment safety

Status: ready-for-human
Resolution: Implemented and verified locally on 2026-10-14. No deployment or push performed.

User authorized implementation2026-10-14 after the in-chat design:
1. Snapshot config when enqueueing. YAML, effective variables/variable-set files,
   resolved domain/network wiring and Deployment options are immutable for the job.
   Both Build and Deploy must use the saved snapshot. Git pins a commit SHA at
   enqueue; ZIP stores an immutable per-job copy. Snapshot data/keys must stay out
   of history/log API responses. Preserve queued deduplication/options, stable
   Compose identities, FIFO/single-worker behavior and existing retained history.
   Old queued records without a snapshot get a documented legacy fallback.
2. Fresh volumes confirmation explicitly warns deletion survives deployment
   failure and asks for a backup. Failed Deployment details retain a warning only
   when named volumes were actually removed (not fetch/build/validation failures).
   Reuse Deployment.warnings and existing Alerts; report actual side effects.
3. Sidebar footer warns when filesystem containing Panel/SQLite data is >=80%
   used. Use latest existing host metrics, show percentage/free space, link to
   Monitoring, persist while scrolling, auto-refresh and represent unavailable or
   stale data honestly. Do not introduce an automatic deploy block or pruning.
4. Retry final database outcome writes without re-executing Docker or processing
   the next job before successful settlement. Back off; on worker shutdown attempt
   the write once, then stop waiting if it fails. Storage loss cannot be made
   recoverable in memory, and this change must not claim exactly-once execution.

Tests cover config edited after enqueue/during build/restart, moving Git branch,
ZIP replacement, old queued records, secret non-disclosure, deduplication,
actual-vs-unperformed volume deletion, transient completion DB failure and no
rerun, disk79.9/80/>80/unknown/stale states, footer position and320pxoverflow.
