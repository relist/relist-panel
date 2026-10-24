# 04 — Warn in preview and deploy about YAML that loses data

**What to build:** two YAML patterns lose data without any sign today. Add them to the warnings
`resolveCompose` already returns, so they show in Preview and the Deployment log (no modal).

1. A writable bind mount under `./src` (`./src`, `./src/...`, or the absolute Compose `src/` path)
   when the Compose has a Source: `WithSourceDirectory` points it at a per-Deployment snapshot, so
   whatever the container writes there is not carried into the next Deployment (item B3).
2. A named volume with an explicit `name:` that is not `external: true`: deleting this Compose
   would remove a volume other stacks may use (item B4). Name the other Compose when one in the
   panel declares the same name.

**Blocked by:** none

**Status:** ready-for-agent

- [ ] Warning texts suggest the fix: "use a named volume" for (1), "declare it `external: true`" for (2)
- [ ] `:ro` binds under `./src` and binds outside `src/` give no warning
- [ ] Tests in `internal/composes`: each pattern warns, each safe variant does not; the sibling
      name lookup names the other Compose

## Source

Stability review, items B3 and B4; discussion on 2026-10-24 chose preview/log warnings over
per-action modals.
