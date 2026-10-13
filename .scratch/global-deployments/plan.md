# Global Deployments implementation plan

**Goal:** Browse the host queue and all retained deployment history from one menu.
**Architecture:** Contextual paginated SQLite queries extend the existing Deployment
API. The menu and page share one queue cache, and both deployment screens share
one log Drawer. Existing scheduling and retention remain intact.
**Tech stack:** Go, SQLite, React 19, Ant Design 6.5.1.
**Spec:** `.scratch/global-deployments/spec.md` (user approved the in-chat scope).

## Global constraints

- Queue is unfiltered and ordered running first, then FIFO.
- History defaults to 20 rows, supports 20/50/100 and filters database context IDs.
- Deployment numbers are Compose-local; every global row includes full context.
- Existing retention is 20 per Compose and the UI states it.
- No horizontal page, log or date-picker overflow down to 320px.

## Review focus

- Same number in different Composes must never share a log or Cancel target.
- A queued filter can shrink while browsing later pages: reconcile requested page.
- Old filter/detail responses cannot overwrite current query/selection.
- Queue completion must refresh history and remove the queued badge.
- Mobile calendar popup bounds matter even when document scrollWidth stays 320.

## Tasks

- [x] Backend (internal/deployments/global.go + tests; existing HTTP/Store integration):
   Add contextual global history query and paginated GET /api/deployments. Extend
   Active queue context while preserving order and existing API fields. Test
   collisions in local numbers, filters, deterministic paging/ties, inclusive
   timestamps, validation, empty arrays, and enriched queue order.
- [x] Shared frontend integration (data/types.ts, data/api.ts, shared queue hook,
   components/DeploymentLogDrawer.tsx, app/paths.ts, routes.tsx, AppShell.tsx):
   Typed global contracts; serialize query with URLSearchParams. Extract existing
   log Drawer with selection key scoped to full Compose path + number and optional
   selected detail polling for global page. Global queue shared SWR cache for badge
   and page, backoff error/idle refresh. Add route/menu/breadcrumb. Regression-test
   old Compose flow and API/hook lifecycle boundaries.
- [x] Global frontend page (pages/DeploymentsPage.tsx + test):
   Contextual full queue, filtered history with server pagination, shared log Drawer,
   filter dependent resets, Alert + Retry and empty/loading states. Latest query
   wins, old history never labelled as new filters. Test pagination/filter requests,
   queue independence, stale responses and Drawer selection identity.
- [x] Integration verification: Go deployments + HTTP API tests, race on changed Go
   package, web suite/typecheck, production frontend build and Go build. Inspect
   actual UI via browser fixtures at desktop/390px/320px, long names/messages/logs,
   queued badge, filtering/page switching and log selection. Independent final
   review. Separate backend, frontend, and embedded asset commits.

## Verification progress

- Initial integration: 509 web tests in 41 files, typecheck, full Go suite/vet/build passed;
  changed deployments race tests passed. First broad Go run showed an untouched
  Docker load cancellation timing failure; narrow 3-run and full-suite rerun passed.
- Review found history pagination clamping needed after filtered results shrink.
- Real browser found mobile RangePicker panels outside viewport; fixing before
  final browser/typecheck/build and separate commits.

- Final fixes verified: 511web tests/typecheck/frontend build pass; independent
  final review has 0 remaining findings. Full browser flow and focused shrinking-page,
  stale-response and calendar tests pass at 1440/390/320 widths and 500/740 mobile
  heights. Calendar viewport anchoring and internal scrolling resolve clipping.
- Backend commit:`0fe8421`. Frontend and embedded assets committed separately.
  No deployment or push is part of this implementation pass.
