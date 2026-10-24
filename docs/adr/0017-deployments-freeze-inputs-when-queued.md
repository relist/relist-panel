# Deployments freeze execution inputs when queued

A long build previously allowed Variables and Domains to change before Deploy,
so one Deployment could build with one configuration and run with another. The
operator chose to freeze inputs when pressing Deploy. Newly queued Deployments
therefore save a private execution snapshot: YAML, effective Variables and
Variable Set files, resolved Domain/network wiring, options and the Source
revision. Build and Deploy consume that saved state; subsequent edits affect
subsequent Deployments.

Git builds pin a commit rather than following a moving branch at execution time.
ZIP uploads are copied into a private tree for each job. A successful Git Build
publishes a private pointer to its materialized tree, so a later skip-build
Deployment can copy the available runtime files without contacting Git or
modifying legacy source mounts. Source changes invalidate that pointer. Source
paths in the saved Compose file refer to the job's tree; other host bind mounts
and Docker image tags retain their existing semantics.

Git revision lookup has a 20-second timeout. Source copying has a 60-second timeout
and limits of 4 GiB and 100,000 files. Capture does not hold a SQLite transaction
through network/filesystem work. Snapshot credentials and variable values remain
server-private and are not added to history/log responses.

The host queue still runs one Deployment at a time and deduplicates one queued
successor per Compose. Repeating enqueue preserves the first snapshot and options.
Queued records predating snapshots cannot recover their original inputs. The
worker captures and persists current inputs once and records a legacy warning.
Per-deployment source files survive restart. Containers can still mount a
Deployment's tree after history pruning, so trees are not deleted with their
Deployment. Instead each Compose's snapshot directories are garbage-collected
once a Deployment settles and once at boot, under the Project gate. A directory
is kept while a queued or running Deployment will use it, while the Source's
materialized tree lives in it, or while it holds a path mounted by any container
of the Compose's Docker project, stopped ones included. Everything else under
`.deployment-sources` is removed. If Docker or the database cannot say what is
referenced, nothing is deleted that round; the remainder is collected by a later
one. Compose teardown still removes whatever is left. This bounds disk use to the
trees in use rather than one per Deploy.

A known final outcome is retried on database write failure before advancing the
worker, without running Docker again. Shutdown bounds that wait; this does not
make Docker/filesystem changes transactional or provide exactly-once execution.
