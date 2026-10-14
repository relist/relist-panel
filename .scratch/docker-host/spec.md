# Docker host reports and cleanup

Status: ready-for-human

Approved scope: add Docker to the sidebar at `/docker`, retain the Images page, show host reports and separate explicitly confirmed prune operations. Implement on `feat/source-builds` and commit backend, frontend, and embedded assets separately.

## Behavior

- Engine version/API, host, storage driver/data directory, running/paused/stopped counts.
- Storage report for images, container writable layers, volumes, default-builder cache and networks. Unknown sizes/references stay unavailable. Category sizes may overlap; do not invent a deduplicated reclaimable image total. Volume estimates include unused named volumes as well as anonymous volumes.
- Container, volume and network inventories, paginated and horizontally contained on narrow screens. Volume inventory comes from `/volumes`, joined to optional `/system/df` usage, so plugin and mount-option volumes stay visible.
- Basic cleanup: dangling images, stopped containers, unused custom networks, unused default-builder cache, anonymous local volumes.
- Advanced cleanup: all unused images (including tagged images), all unused local volumes (including named volumes).
- Every prune names its host-wide consequences and requires `PRUNE`, or `DELETE VOLUMES` for volume removal. API validates the confirmation independently and rejects unknown fields/kinds.
- Volumes require Engine API 1.42+; explicitly select anonymous/all filters and use the daemon's supported API version. Never force-remove attached resources.
- Backend excludes pruning from queued/running Deployments, whole worker execution (including Build), Project lifecycle/volume operations and actual `PUT /api/images` loading. Prune rejects busy work; new work waits for a normal prune. Metadata/history reads and cancellation remain available.
- Refresh/read failures stay on screen and disable pruning. Successful reports cache for one minute; explicit refresh bypasses cache. Failed refresh invalidates cached success, so retries must read Docker again.
- Show actual reclaimed bytes and removed entries after a successful mutation. Refresh after either outcome; no automatic retries of destructive calls.
- Browser disconnect does not cancel the server's prune request. The bounded request lasts at most five minutes. If POST transport/result decoding fails, completion is unknown: pause Docker work and additional pruning. Report the pause; resume only with `RESUME DOCKER` after the operator verifies deletion has finished with the daemon.

## Limits

- Host coordination is local to this panel process. Other Docker clients are not coordinated. The maintenance state is memory-only; restarting the panel clears it and does not prove an earlier prune finished. Verify daemon deletion completion before restarting/resuming after an uncertain result.
- Reports are successive daemon reads rather than one atomic snapshot. Docker decides eligibility at prune time. Estimated bytes and inventory are not a dry run or a promise of which exact IDs will be removed.
- Cache report/prune covers Docker's default builder, not independently configured buildx builders.
- Named volume deletion is permanent and includes data from removed Composes; a later failed deploy cannot restore it.
- Tests use fake Docker sockets and browser API fixtures; validation never prunes the live host.

## Verification

Completed locally on 2026-10-14; no live prune, deployment or push.

- `go test ./... -count=1`: all packages pass.
- `go test -race ./internal/dockerhost ./internal/dockerexec ./internal/deployments -count=1`: pass.
- Relevant HTTP race tests (`TestDockerHost|TestProjectOperations|TestLifecycle|TestImage`): pass.
- `go vet ./...` and embedded binary build: pass.
- Frontend full suite: 550 tests in 45 files pass. Final page tests (7), typecheck and production rebuild pass after layout refinements.
- Chrome with API fixtures: desktop 1440 and mobile 390/320 report, volume inventory, volume/recovery confirmations, advanced cleanup and failed refresh. Document width equals viewport in every state; no page errors; no mutation clicked.
- Local verification artifacts: `/tmp/relist-docker-browser-report.json`, `/tmp/relist-docker-browser-run.log`, `/tmp/relist-docker-go-tests-final.log`, `/tmp/relist-docker-race.log`, `/tmp/relist-docker-http-race.log`.

 Independent review caught and verified fixes for omitted volume inventory, wrong image-load method, swallowed SWR refresh failures, stale cache resurrected by retries, and ambiguous timeout completion.
