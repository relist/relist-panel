# 20 — Volume Explorer can page past the first 10,000 entries

**What to build:** since ticket 12 a listing stops at 10,000 entries and the Explorer cannot reach the rest. Page by name cursor: the helper's `list` takes an optional `after` name and returns the 10,000 names that sort after it, `truncated` and `next` (the last name when truncated); `GET …/volumes/list` passes `after`; the Explorer shows "Next 10,000" and "Back to the start" controls and which range it shows.

**Blocked by:** none

**Status:** ready-for-agent

## Source

Parked during the stability-review implementation (Task 13), approved by the operator 2026-10-25.
