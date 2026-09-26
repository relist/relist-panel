# 05 — Read endpoints over `docker exec`

**What to build:** The HTTP surface the Volume Explorer reads through, each request proxying exactly
one `docker exec` into the handle's helper.

**Blocked by:** 01, 03, 04

**Status:** needs-triage

- [ ] Endpoints for list, stat, read, and the handle's own state (running, idle countdown, uid/gid, mode)
- [ ] Each call is one `docker exec -i <container> relist-volhelper <op> -- <path>`, with the path as its own argv element after `--`
- [ ] Paths are always relative to the Volume root, and a path from a request is never interpolated into a shell string — there is no shell in the path from request to helper
- [ ] `read` streams raw bytes with `io.Copy` straight into the response: nothing buffered whole in memory, nothing written to disk
- [ ] A refusal from the helper (escaping path, missing entry) surfaces as the panel's normal JSON error carrying the helper's own message, not a generic failure
- [ ] HTTP-seam tests with a faked exec asserting the exact argv for each operation, including a path beginning with `-` and a path containing a newline
