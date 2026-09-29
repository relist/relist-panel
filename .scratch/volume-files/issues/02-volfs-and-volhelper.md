# 02 — `volfs`, the tar.gz writer, and the `relist-volhelper` binary

**What to build:** The path-safe filesystem layer, the tar.gz writer, and the helper binary that
exposes both over an argv/stdio protocol. No Docker on this ticket: `internal/volfs` is a real
package with real tests against a real temp directory. This is several sessions of work, not one.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] `internal/volfs` implements list, stat, read, write and delete over one `os.OpenRoot("/data")`; path arguments are relative-only, and containment is enforced by `os.Root`, not by string checks — `..`, absolute paths and `dir/../../x` all fail with `path escapes from parent`
- [ ] Directory listings go `root.Open(".")` → `*os.File.ReadDir(-1)` (`os.Root` has no `ReadDir` of its own in Go 1.27). Raw names come from `ReadDir`, never from parsing a listing, so a filename containing a newline round-trips intact
- [ ] Symlink semantics match the spec's table: listed by `ReadDir`, target reported by `Lstat` + `Readlink`, followed by `Open` only when the target stays inside the root; the rest are refused with a clear message
- [ ] The tar.gz writer is stdlib only (`archive/tar`, `compress/gzip`), writes to a non-seekable writer with no temp file, and sets entry names explicitly to the path relative to the root — slash-separated, never absolute, never containing a `..` segment
- [ ] Symlinks are stored as symlinks, faithfully; hardlinks are written as regular files
- [ ] `cmd/relist-volhelper` dispatches `list`, `stat`, `read`, `write`, `delete` and `archive --format tar.gz`; the path is read after `--` so a name starting with `-` cannot be read as a flag; metadata operations return JSON on stdout, while `read` and `archive` return raw bytes
- [ ] The helper installs no signal handler, so pid1 dies on SIGTERM by default and a stop stays fast
- [ ] The Dockerfile builds both the panel and `relist-volhelper` into the panel's image; the panel resolves that image with `docker inspect $(hostname)` → `.Config.Image`, falling back to `RELIST_IMAGE` — no registry, no runtime pull, no version skew between panel and helper
- [ ] `go.mod` moves to `go 1.24` minimum, `1.25` preferred for the wider `os.Root` surface. Verified necessary: under a `go 1.23.0` directive `go build` accepts `os.Root`, but `go vet` and `go test` both fail with `os.OpenRoot requires go1.24 or later`
- [ ] Tests cover traversal (`..`, absolute, `dir/../../x`, percent-encoded), a symlink that stays inside the root, ones that point outside (relative and at `/etc/passwd`), newline-bearing names, and a tar.gz round-trip read back with `archive/tar` and `compress/gzip` asserting every entry name is relative and `..`-free and that a symlink survived as a symlink
