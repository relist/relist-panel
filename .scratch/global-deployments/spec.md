# Global Deployments

Status: ready-for-human

Add Deployments below Projects in the side menu with a badge counting queued work.
The /deployments page shows the full host queue above paginated history across all
Composes. Queue order remains running first, then FIFO. History filters Project,
Environment, Compose, status and inclusive queuedAt date range. Context names and
IDs come from the database, and every row distinguishes Compose-local numbers.
Open logs in the same right Drawer as Compose deployments, with steps, warnings,
failures and Cancel. Preserve Compose tab behavior, stop log reads when closed or
leaving a page, and prevent stale responses when selection/filters change. Respect
Ant Design 6.5.1, existing tokens and no long-text/page overflow down to 320px.

Keep existing retention (20 per Compose) and disclose it in the UI. This feature
browses all retained records; it does not restore pruned/deleted history or change
queue scheduling. Queue filters are independent from History filters.

API contract: GET /api/deployments with page (default1), pageSize (default20,
max100), optional positive projectId/environmentId/composeId, a single known
status, from/to RFC3339 timestamps on queuedAt (inclusive). Invalid query returns
400. Return {items,total,page,pageSize}; items are existing Deployment JSON plus
projectId/projectName/environmentId/environmentName/composeName. Stable newest
ordering queuedAt DESC,id DESC. Count and page use identical predicates.
GET /api/deployments/queue retains its existing array/fields and adds the same
Project/Environment context IDs/names. Existing per-Compose routes stay intact.

## Verification

Implemented and reviewed on 2026-10-13. Global queue and retained-history API,
menu/badge, filter/pagination flow and shared log Drawer are complete. Existing
20-per-Compose retention stays unchanged. 511web tests in 41 files, typecheck,
frontend build, full Go suite/vet/build and scoped deployments race passed.

Browser-only fixtures verified the actual app at 1440/390/320px: queue/badge,
server pagination and hierarchical/date filters, independent queue, cancellation,
completion, duplicate local numbers, late response isolation and polling cleanup.
Review fixes cover shrinking filtered pages, mobile title spacing and fully
accessible calendars at 320/390px widths and 500/740px heights. Final review is clean.
Running panel.localhost has not been deployed or tested with its logged-in data.
