# 15 — Prune confirmation names Relist-owned volumes and images

**What to build:** Stop is `docker compose down`, so a stopped Compose's named volumes are
immediately "dangling" (verified) and are what "Prune all unused volumes" deletes; built or loaded
images likewise count as unused. The page already warns in general terms; list the volumes and
images whose `com.docker.compose.project` label belongs to a panel Compose, by Compose name, in the
confirmation.

**Decision (operator, 2026-10-25):** skip panel-owned volumes by default instead of only naming them. A volume is panel-owned when its `com.docker.compose.project` label is a panel Compose's deploy project name, or when a panel Compose's YAML names it (including `external: true` volumes, which the editor guide recommends for shared data and which carry no label). Images are skipped the same way (ruling: an uploaded image cannot be pulled again): an image a panel Compose's YAML names literally in `image:`, or one carrying a panel project label. The confirmation lists what will be deleted and what is skipped, by Compose; a separate opt-in with a stronger confirmation word includes the panel-owned ones.

**Blocked by:** none

**Status:** ready-for-agent

## Source

Stability review, item B6. Not discussed on 2026-10-24.
