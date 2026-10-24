# 15 — Prune confirmation names Relist-owned volumes and images

**What to build:** Stop is `docker compose down`, so a stopped Compose's named volumes are
immediately "dangling" (verified) and are what "Prune all unused volumes" deletes; built or loaded
images likewise count as unused. The page already warns in general terms; list the volumes and
images whose `com.docker.compose.project` label belongs to a panel Compose, by Compose name, in the
confirmation.

**Blocked by:** none

**Status:** needs-triage

## Source

Stability review, item B6. Not discussed on 2026-10-24.
