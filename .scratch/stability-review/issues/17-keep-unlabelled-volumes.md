# 17 — Delete keeps an existing volume that Compose did not create

**What to build:** a destructive down (Delete, teardown, fresh-volumes Deploy) removes a named volume that exists but has no `com.docker.compose.project` label, because the keep rule from ticket 03 only looks at a label that is present. Such a volume was made by hand (`docker volume create`) and declared without `external: true`; Compose warns about it at `up`. Keep it, with reason `not created by Docker Compose`. The removal plan shows it under kept volumes automatically.

**Blocked by:** none

**Status:** closed

## Source

Parked during the stability-review implementation (ruling on Task 4), approved by the operator 2026-10-25.
