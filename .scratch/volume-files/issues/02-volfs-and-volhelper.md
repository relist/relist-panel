# 02 — `volfs`, the tar.gz writer, and the `relist-volhelper` binary

**What to build:** The path-safe filesystem layer, the tar.gz writer, and the helper binary that
exposes both over an argv/stdio protocol. No Docker on this ticket: `internal/volfs` is a real
package with real tests against a real temp directory. This is several sessions of work, not one.

**Blocked by:** None — can start immediately.

**Status:** ready-for-human

- [x] `internal/volfs` implements list, stat, read, write and delete over one `os.OpenRoot("/data")`; path arguments are relative-only, and containment is enforced by `os.Root`, not by string checks — `..`, absolute paths and `dir/../../x` all fail with `path escapes from parent`
- [x] Directory listings go `root.Open(".")` → `*os.File.ReadDir(-1)` (`os.Root` has no `ReadDir` of its own in Go 1.27). Raw names come from `ReadDir`, never from parsing a listing, so a filename containing a newline round-trips intact
- [x] Symlink semantics match the spec's table: listed by `ReadDir`, target reported by `Lstat` + `Readlink`, followed by `Open` only when the target stays inside the root; the rest are refused with a clear message
- [x] The tar.gz writer is stdlib only (`archive/tar`, `compress/gzip`), writes to a non-seekable writer with no temp file, and sets entry names explicitly to the path relative to the root — slash-separated, never absolute, never containing a `..` segment
- [x] Symlinks are stored as symlinks, faithfully; hardlinks are written as regular files
- [x] `cmd/relist-volhelper` dispatches `list`, `stat`, `read`, `write`, `delete` and `archive --format tar.gz`; the path is read after `--` so a name starting with `-` cannot be read as a flag; metadata operations return JSON on stdout, while `read` and `archive` return raw bytes
- [x] The helper installs no signal handler, so pid1 dies on SIGTERM by default and a stop stays fast
- [x] The Dockerfile builds both the panel and `relist-volhelper` into the panel's image; the panel resolves that image with `docker inspect $(hostname)` → `.Config.Image`, falling back to `RELIST_IMAGE` — no registry, no runtime pull, no version skew between panel and helper
- [x] `go.mod` moves to `go 1.24` minimum, `1.25` preferred for the wider `os.Root` surface. Verified necessary: under a `go 1.23.0` directive `go build` accepts `os.Root`, but `go vet` and `go test` both fail with `os.OpenRoot requires go1.24 or later`
- [x] Tests cover traversal (`..`, absolute, `dir/../../x`, percent-encoded), a symlink that stays inside the root, ones that point outside (relative and at `/etc/passwd`), newline-bearing names, and a tar.gz round-trip read back with `archive/tar` and `compress/gzip` asserting every entry name is relative and `..`-free and that a symlink survived as a symlink

## Comments

**session 1/3:** `volfs` core landed (boxes 1–3, 9; the volfs half of box 10). Evidence under
`golang:1.27-alpine`: `gofmt -l .` silent, `go vet ./...` clean, `go test ./...` all green
(volfs: 7 tests / 20 sub-cases). `go.mod` is `go 1.25.0`, the lowest directive vet accepts:
`os.Root.Readlink` and `os.Root.RemoveAll` report "requires go1.25 or later" under `go 1.24.0`,
and `go 1.23.0` already fails on `os.OpenRoot`. Tests assert os.Root's own
`path escapes from parent` for list/stat/read/write/delete across `..`, absolute paths and
`dir/../../x`, that escaping operations touch nothing outside the root, and that percent-encoded
names stay plain components. Still open for sessions 2/3: the tar.gz writer (4, 5, archive half of
10), `relist-volhelper` (6, 7), and its Dockerfile stage (8).

**session 2/3:** the tar.gz writer landed (boxes 4, 5, and the archive half of 10):
`internal/volfs/archive.go` adds `func (fs *FS) Archive(name string, dst io.Writer) error` —
stdlib `archive/tar` + `compress/gzip` only, no temp file; every header is built from `Entry`, so
names are always the root-relative path and never a `tar.FileInfoHeader` base name, and the walk
uses `List` with file bytes streamed straight from `Read`. Symlinks become `TypeSymlink` with the
target verbatim (outside-root links included, never followed); hardlinks stream as regular files;
directories get entries, so `Archive("empty")` is a single `empty/` entry while the root itself is
never given an invented `.` entry; a FIFO, socket or device is skipped rather than opened (a FIFO
read would block), and any read or write failure aborts the walk. Evidence under
`golang:1.27-alpine`: `gofmt -l .` silent, `go vet ./...` clean, `go test ./...` all green;
`internal/volfs` now runs 15 top-level tests, 8 of them new archive tests — a tree streamed
through an `io.Pipe` and read back with `archive/tar` + `compress/gzip` (1 MB random binary, nested
file and text file byte-identical by sha256), every entry name relative and `..`-free, a symlink
surviving as a symlink with its target, an `os.Link` hardlink arriving as a regular file with the
right bytes, a newline-bearing name intact, an empty directory surviving, a single file as a
one-entry archive named as its path, `..`/absolute/missing paths refused before any byte reaches
`dst`, and a dead destination aborting with `io.ErrClosedPipe`. Still open for session 3:
`relist-volhelper` (6, 7) and its Dockerfile stage (8).

**session 3/3:** helper and image landed (boxes 6–8). `cmd/relist-volhelper/main.go` is a thin
binary: `main` calls `run(args, stdin, stdout, stderr, rootDir) int`, which parses the protocol's
exact argv shape, refuses an absolute path *before* `volfs.Open` (tests prove it with an absent
root), opens the root once and dispatches `list`/`stat` (JSON on stdout), `read`/`archive` (raw
bytes), `write` (stdin) and `delete`. `--` is mandatory, so a name starting with `-` is a path and
never a flag; unknown ops, missing args and wrong shapes print usage to stderr and exit 2, open/op
failures exit 1, and stdout carries only operation bytes or JSON. No signal handler, with the
reason in a comment at `main` (pid1 must keep default SIGTERM: ~1.1 s stop vs ~10.1 s). The
Dockerfile now builds `./cmd/relist-volhelper` in the go-builder stage and copies it to
`/usr/local/bin/relist-volhelper`, the exact path `volume_runner.go` invokes; image resolution
(`docker inspect $(hostname)` → `.Config.Image`, `RELIST_IMAGE` fallback) was already implemented
in `internal/dockerexec/volume_runner.go` at commit `b467f0c` and was not reimplemented.
Evidence under `golang:1.27-alpine`: `gofmt -l .` silent, `go vet ./...` clean, `go test ./...` all
green (14 new helper tests). `docker build -t relist-panel:voltest .` succeeds end to end and the
built image was exercised live with the panel's own run spec: `list -- .` returned the JSON array,
`read -- a.txt` returned raw bytes, `archive --format tar.gz -- .` piped through `tar -tzvf` listed
`a.txt`, `sub/`, `sub/b.txt`, and `read -- /etc/passwd` was refused before the mount with exit 2.
Sizes confirm the helper is its own small binary: 4.6 MB against the panel's 25.1 MB. Nothing left
open on this ticket.
