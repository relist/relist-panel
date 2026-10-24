# 03 — Delete and teardown keep named volumes that another Compose owns or declares

**What to build:** `docker compose down -v` removes every non-external volume in `volumes:`,
including one with an explicit `name:` that another Compose created or also declares. Docker only
refuses while a container exists, and the panel's Stop is `down`, which removes containers.
Verified on Docker 29.8 / Compose v5.5.1: with B stopped, deleting A removed `relistreview-shared`
even when its `com.docker.compose.project` label was B (Compose only warns at `up`). Delete,
Project/Environment teardown and fresh-volumes deploys must not destroy another Compose's data.

Decision (2026-10-24): **skip and report**, not refuse. The Compose is still deleted; the shared
volumes stay and are named in the response.

**Blocked by:** none

**Status:** ready-for-agent

- [ ] Before the destructive step, resolve the candidates from `docker compose config --format json`
      (as `DownWithVolumesTracked` already does). A volume is kept when it is external, when its
      Docker label `com.docker.compose.project` is not this Compose's project, or when another
      Compose row in the panel declares the same resolved volume name
- [ ] When any volume is kept, run `down` (no `-v`) and remove the remaining candidates one by one
      with `docker volume rm` (never forced); otherwise keep today's `down -v`
- [ ] The Delete response and the teardown error/success body list kept volumes; fresh-volumes
      deploys log them in the Deployment log
- [ ] Tests (fake CLI): owned volume removed; volume labelled for another project kept; volume
      declared by a sibling Compose kept; external never a candidate
- [ ] Optional e2e (spec E2): two Composes share `name: x`, stop one, delete the other, volume survives

## Source

Stability review, item B4 (`.scratch/stability-review/spec.md`). Reproduction steps and compose
file: `.scratch/stability-review/verification/b4-docker-compose.yml`.
