# Deploy identity is immutable, frozen at Compose creation

A Compose's deploy identity — its data-dir path `dataDir/<project-slug>/<env-slug>/<compose-slug>/`
and its `docker compose -p` project name `<p>-<e>-<c>` (ADR-0002) — used to be recomputed from the
*current* Project and Environment names on every lifecycle action. Parent names are mutable;
deployed containers are not. Renaming a Project made every action address a path that no longer
matched the running stack: delete skipped `DownWithVolumes` and orphaned containers, stop/restart/
logs returned "not deployed", status reported running stacks as Stopped.

**Decision:** identity is stored on the Compose row at creation and only read afterwards. Renames
never propagate to deployed stacks, and Project slugs are globally unique while Environment slugs
are unique within their Project, so no two Composes can share an identity.

Because identity is frozen, renaming propagation would mean moving files under the data dir and
re-pointing running containers — a deploy-time migration feature, not a rename side effect. That is
deliberately out of v1 scope (ADR-0001) and should stay out unless a user-facing "rename re-home"
feature is designed on its own. Future architecture reviews should not re-suggest making identity
name-derived again.
