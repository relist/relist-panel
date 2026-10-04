# Deploy identity is immutable, frozen at Compose creation

> **Revised by ADR-0011:** immutability remains; new Composes use database IDs. The original claim below that parent-name uniqueness guarantees deployment uniqueness was disproved by delimiter ambiguity and renamed-name reuse. Existing stacks retain their identities.

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

**Known edge, accepted:** the one-time backfill fills a legacy row's empty slugs from the parents'
*current* names. A Project or Environment that was renamed *before* upgrading to stored identities
gets its post-rename slug frozen, leaving the stack deployed under the pre-rename name unreachable
through the panel — the same symptom class this ADR fixes, now permanent for that row. Composes
created after this change are unaffected, and renames after the backfill are safe because slugs are
only ever read, never re-derived.
