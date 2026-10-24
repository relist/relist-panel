# 08 — Garbage-collect Deployment source snapshots

**What to build:** each Deployment of a Compose with a Source copies src/ (up to 4 GiB) to
`<compose>/.deployment-sources/source-*`. Only cancel paths remove one; history pruning does not.
Verified: six deploys with Keep=2 left six snapshot directories. Disk grows until the host fails.
ADR-0017 accepted retention "until Compose teardown" without a bound; change that.

**Blocked by:** none

**Status:** closed

- [ ] After a Deployment settles, and once at boot, remove every snapshot directory of that Compose
      except: those of queued/running Deployments, the one `compose_source_materializations`
      points at, and any mounted by the Compose's current containers (Docker `Mounts[].Source` of
      containers labelled with its project)
- [ ] A Docker lookup failure skips the GC for that round (never delete when unsure)
- [ ] Amend ADR-0017's retention paragraph
- [ ] Tests: six deploys leave only referenced directories; a directory a container mounts is kept;
      a lookup error deletes nothing

## Source

Stability review, item C1. Reproduction:
`.scratch/stability-review/verification/deployments_review_verify_test.go`. The live install had
no `.deployment-sources` on 2026-10-24.
