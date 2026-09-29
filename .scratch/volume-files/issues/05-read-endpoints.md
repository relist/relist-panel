# 05 — Read endpoints and backup download, one run per operation

**What to build:** The HTTP surface the Volume Explorer reads through. Each request is one
`docker run --rm` invoking the helper: list, stat and read, plus a backup download that streams
tar.gz straight from the helper's stdout into the HTTP response.

**Blocked by:** 02, 03, 13

**Status:** ready-for-agent

- [ ] Endpoints for list, stat and read, each one `docker run --rm` with the path as an argv element after `--`
- [ ] `read` streams raw bytes straight into the response with `io.Copy`: nothing buffered whole in memory, nothing written to disk
- [ ] `GET` backup streams the helper's tar.gz stdout into the response with no temp file anywhere, so a 50 GB Volume never becomes a 50 GB file in the data directory; a single file is served as a plain download, not wrapped in an archive
- [ ] `Content-Disposition` follows RFC 6266: control characters stripped, and `filename*` percent-encoded for any non-ASCII name
- [ ] Paths are always relative to the Volume root, and a path from a request is never interpolated into a shell string — there is no shell in the path from request to helper
- [ ] A refusal from the helper (escaping path, missing entry) surfaces as the panel's normal JSON error carrying the helper's own message, not a generic failure
- [ ] HTTP-seam tests fake the runner and assert the exact argv for each operation, including a path beginning with `-` and a path containing a newline
