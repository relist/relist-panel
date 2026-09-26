# 02 — `volfs` package and the `relist-volhelper` binary

**What to build:** The path-safe filesystem layer and the helper that exposes it over an argv/stdio
protocol. No Docker involved on this ticket — this is a real package with real tests against a real
temp directory. Everything downstream depends on `os.Root` refusing to leave the Volume root, so
that guarantee is established and tested here, before anything is wired to a container.

**Blocked by:** 01

**Status:** needs-triage

- [ ] `internal/volfs` exposes the operations over one `os.OpenRoot("/data")` handle: list, stat, read, write, mkdir, rename, delete, archive
- [ ] Path arguments are validated as relative, and containment is enforced by `os.Root`, not by string checks — `..`, absolute paths, and `dir/../../x` all fail with `path escapes from parent`
- [ ] Directory listings go `root.Open(".")` → `*os.File.ReadDir(-1)`; `os.Root` has no `ReadDir` method of its own in Go 1.27, and no operation ever shells out to `ls` or parses a listing
- [ ] Symlink semantics match the spec's table: listed by `ReadDir`, target reported by `Lstat` + `Readlink`, followed by `Open` only when the target stays inside the root
- [ ] A filename containing a newline round-trips intact through list → read → rename → delete
- [ ] `archive` streams tar.gz or zip to stdout with no temp file; entry names are the path relative to the root, slash-separated, never absolute, never containing a `..` segment
- [ ] Symlinks are stored as symlinks in both archive formats; hardlinks are written as regular files
- [ ] `cmd/relist-volhelper` dispatches `list`, `stat`, `read`, `write`, `mkdir`, `rename`, `delete`, `archive`; paths are read after `--` so a name starting with `-` cannot be read as a flag
- [ ] Metadata operations return JSON on stdout; `read` and `archive` return raw bytes
- [ ] The helper installs no SIGTERM handler, so `docker stop` does not wait out Docker's 10 s grace period (measured: 10.1 s trapped-and-ignored vs 1.1 s handled)
- [ ] `go.mod` moves to `go 1.24` minimum. Verified necessary: under a `go 1.23.0` directive `go build` accepts `os.Root` but `go vet` and `go test` both fail with `os.OpenRoot requires go1.24 or later`, which would break the suite
- [ ] Tests cover traversal (`..`, absolute, `dir/../../x`, percent-encoded), a symlink pointing inside the root, one pointing outside at a relative path, one pointing at `/etc/passwd`, newline-bearing names, and an archive round-trip read back with `archive/tar` and `archive/zip` asserting every entry name is relative and `..`-free and that symlinks survived as symlinks
