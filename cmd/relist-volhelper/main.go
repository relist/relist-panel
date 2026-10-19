// Command relist-volhelper is the Volume helper container's entire program:
// one run per operation, argv in and bytes over stdin/stdout, no server, no
// socket, no network (ADR-0010, the spec's Protocol). The panel's runner
// invokes it out of the panel's own image with exactly one Volume mounted at
// /data (the spec's Run spec):
//
//	relist-volhelper list    -- <path>
//	relist-volhelper stat    -- <path>
//	relist-volhelper read    -- <path>
//	relist-volhelper write   [--expect <bytes>] -- <path>
//	relist-volhelper delete  -- <path>
//	relist-volhelper mkdir   -- <path>
//	relist-volhelper create  -- <path>
//	relist-volhelper archive --format tar.gz -- <path>
//	relist-volhelper extract -- <path>
//
// Paths are relative to /data and are only ever read after `--`, so a name
// starting with `-` cannot be taken for a flag. list and stat answer with
// JSON on stdout, read and archive with raw bytes, and write and extract
// consume stdin; every failure goes to stderr with a non-zero exit. All the
// dispatch lives in run, below the process boundary, so the whole protocol is
// tested without a container.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"relist-panel/internal/volfs"
	"relist-panel/internal/volumes"
)

// dataDir is where the panel mounts the one Volume this run may touch — the
// declared source, nothing else, ever (the spec's Run spec).
const dataDir = "/data"

// usage is printed for every malformed invocation: an unknown op, a missing
// argument, or anything that is not exactly the protocol's shape.
const usage = `usage:
  relist-volhelper list    -- <path>
  relist-volhelper stat    -- <path>
  relist-volhelper read    -- <path>
  relist-volhelper write   [--expect <bytes>] -- <path>
  relist-volhelper delete  -- <path>
  relist-volhelper mkdir   -- <path>
  relist-volhelper create  -- <path>
  relist-volhelper archive --format tar.gz -- <path>
  relist-volhelper extract -- <path>
  relist-volhelper pack    -- <path>
  relist-volhelper unpack  -- .
  relist-volhelper size    -- <path>
  relist-volhelper free    -- .
  relist-volhelper probe   -- <path>
<path> is relative to the Volume root mounted at /data.
--expect is a write's declared size in bytes, checked against the Volume's free space before stdin is read.
mkdir creates one directory; create creates one empty file. Neither replaces an existing entry.
extract consumes a tar.gz on stdin and writes it into the existing directory <path>.
pack and unpack move a whole Volume for a server migration: an uncompressed tar that keeps
owners and modes, with no size cap. size and free answer {"bytes":N}.
probe answers {"exists":B,"empty":B}: empty only for a directory with no entries.
`

// The helper's exit codes: 0 for success, 1 when an operation failed and 2
// when the invocation itself was refused. The typed refusals — 3 for want of
// space, 4 for a refused archive and 5 for a name already taken — are
// declared in volumes, next to the error types the panel maps them to, so
// helper and runner cannot skew. Any non-zero code is a failure to the panel;
// the split only sharpens the helper's own diagnostics.
const (
	exitOK             = 0
	exitError          = 1
	exitUsage          = 2
	exitRefused        = volumes.RefusalExitCode
	exitArchiveRefused = volumes.ArchiveRefusedExitCode
	exitExists         = volumes.ExistsExitCode
)

func main() {
	// No signal handler, on purpose. As pid1 the helper must keep Go's
	// default SIGTERM behaviour — die immediately — so a stop stays fast:
	// measured at ~1.1 s, against ~10.1 s when pid1 traps or ignores SIGTERM
	// (the spec's execution costs). Nothing here calls signal.Notify.
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, dataDir, (*volfs.FS).FreeBytes))
}

// run is the helper's whole protocol: parse one argv, refuse an absolute path
// before any filesystem call, open the Volume root once, and dispatch exactly
// one operation. It returns the process exit code. freeSpace reads the
// Volume's free space — volfs's statfs query in production, injected in tests
// so a refusal and a mid-stream abort are exercised without filling a disk.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, rootDir string, freeSpace func(*volfs.FS) (uint64, error)) int {
	cmd, err := parse(args)
	if err != nil {
		fmt.Fprintf(stderr, "relist-volhelper: %v\n\n%s", err, usage)
		return exitUsage
	}

	// Absolute paths are refused here, before os.OpenRoot, so the refusal
	// never depends on the mount existing or on os.Root having to catch it.
	// os.Root would refuse the path too — defense in depth, not the guard.
	if filepath.IsAbs(cmd.name) {
		fmt.Fprintf(stderr, "relist-volhelper: absolute path %q is not allowed: paths are relative to the Volume root\n", cmd.name)
		return exitUsage
	}

	vol, err := volfs.Open(rootDir)
	if err != nil {
		fmt.Fprintf(stderr, "relist-volhelper: %v\n", err)
		return exitError
	}
	defer vol.Close()

	switch cmd.op {
	case "list":
		entries, err := vol.List(cmd.name)
		if err != nil {
			return fail(stderr, err)
		}
		if err := json.NewEncoder(stdout).Encode(entries); err != nil {
			return fail(stderr, fmt.Errorf("write listing: %w", err))
		}
		return exitOK
	case "stat":
		entry, err := vol.Stat(cmd.name)
		if err != nil {
			return fail(stderr, err)
		}
		if err := json.NewEncoder(stdout).Encode(entry); err != nil {
			return fail(stderr, fmt.Errorf("write stat: %w", err))
		}
		return exitOK
	case "read":
		return failOn(stderr, vol.Read(cmd.name, stdout))
	case "write":
		return runWrite(stderr, vol, stdin, cmd, freeSpace)
	case "delete":
		return failOn(stderr, vol.Delete(cmd.name))
	case "mkdir":
		return runNewEntry(stderr, vol.Mkdir(cmd.name, 0o755))
	case "create":
		return runNewEntry(stderr, vol.Create(cmd.name, 0o644))
	case "archive":
		// parse pinned the format: tar.gz is the only one the helper speaks.
		return failOn(stderr, vol.Archive(cmd.name, stdout))
	case "extract":
		return runExtract(stderr, vol, stdin, cmd)
	case "pack":
		return failOn(stderr, vol.Pack(cmd.name, stdout))
	case "unpack":
		if err := vol.Unpack(stdin); err != nil {
			if errors.Is(err, volfs.ErrUnsafeArchive) || errors.Is(err, volfs.ErrInvalidArchive) {
				fmt.Fprintf(stderr, "relist-volhelper: %v\n", err)
				return exitArchiveRefused
			}
			return fail(stderr, err)
		}
		return exitOK
	case "size":
		n, err := vol.Size(cmd.name)
		if err != nil {
			return fail(stderr, err)
		}
		return writeBytes(stdout, stderr, int64(n))
	case "probe":
		return runProbe(stdout, stderr, vol, cmd.name)
	case "free":
		n, err := freeSpace(vol)
		if err != nil {
			return fail(stderr, err)
		}
		return writeBytes(stdout, stderr, int64(n))
	default:
		// Unreachable while parse and this switch agree; a failure here is a
		// bug, and must still be a non-zero exit, not a silent success.
		fmt.Fprintf(stderr, "relist-volhelper: unhandled operation %q\n\n%s", cmd.op, usage)
		return exitUsage
	}
}

// command is one parsed helper invocation: the op, the path after `--`, and —
// for write — the declared upload size from `--expect`, when the panel knows
// it. The value alone cannot say whether the flag was given, because zero is a
// legal declared size, so declared records its presence.
type command struct {
	op       string
	name     string
	expect   int64
	declared bool
}

// parse accepts exactly one argv shape per op: `-- <path>` for every op,
// `--expect <bytes> -- <path>` additionally for write, and `--format tar.gz --
// <path>` for archive. `--` is mandatory, so a name starting with `-` is
// always a path and never a flag; the flag order is fixed because the panel is
// the only caller and emits precisely these shapes.
func parse(args []string) (command, error) {
	if len(args) == 0 {
		return command{}, errors.New("missing operation")
	}
	cmd := command{op: args[0]}
	rest := args[1:]

	switch cmd.op {
	case "list", "stat", "read", "delete", "extract", "mkdir", "create", "pack", "size", "probe":
		// No extra flags: the path follows immediately after `--`.
	case "unpack", "free":
		// Whole-Volume operations: the path must be the root.
		if len(rest) == 2 && rest[0] == "--" && rest[1] != "." {
			return command{}, fmt.Errorf("%s takes `-- .`", cmd.op)
		}
		// No extra flags: the path follows immediately after `--`.
	case "write":
		// An upload's declared byte count, so the helper can refuse a write
		// that cannot fit before it reads any stdin.
		if len(rest) >= 2 && rest[0] == "--expect" {
			expect, err := strconv.ParseInt(rest[1], 10, 64)
			if err != nil || expect < 0 {
				return command{}, fmt.Errorf("--expect takes a non-negative byte count, got %q", rest[1])
			}
			cmd.expect, cmd.declared = expect, true
			rest = rest[2:]
		}
	case "archive":
		if len(rest) < 2 || rest[0] != "--format" {
			return command{}, errors.New("archive requires --format tar.gz")
		}
		if rest[1] != "tar.gz" {
			return command{}, fmt.Errorf("unsupported archive format %q: the only format is tar.gz", rest[1])
		}
		rest = rest[2:]
	default:
		return command{}, fmt.Errorf("unknown operation %q", cmd.op)
	}

	if len(rest) != 2 || rest[0] != "--" {
		return command{}, fmt.Errorf("%s takes `-- <path>`", cmd.op)
	}
	cmd.name = rest[1]
	return cmd, nil
}

// errNoSpace is the guard's own refusal, a sentinel so runWrite can tell a
// mid-stream overflow — which removes the partial file and exits 3 — from any
// other io failure, which stays exit 1.
var errNoSpace = errors.New("stream exceeds the Volume's free space")

// runWrite is the protocol's only consuming operation. It measures the
// Volume's free space once, refuses a declared size that cannot fit before
// stdin or the target is touched (the spec's Caps), then guards the stream
// against the same measurement. volfs.Write creates and truncates the target
// before the copy, so the original is already replaced once the copy begins: a
// guarded abort removes the partial file rather than leaving a truncated one,
// the removal is best-effort, and the refusal still exits 3.
func runWrite(stderr io.Writer, vol *volfs.FS, stdin io.Reader, cmd command, freeSpace func(*volfs.FS) (uint64, error)) int {
	free, err := freeSpace(vol)
	if err != nil {
		return fail(stderr, fmt.Errorf("write %q: check free space: %w", cmd.name, err))
	}
	if cmd.declared && uint64(cmd.expect) > free {
		fmt.Fprintf(stderr, "relist-volhelper: write %q needs %d bytes but the Volume has only %d free\n", cmd.name, cmd.expect, free)
		return exitRefused
	}

	if err := vol.Write(cmd.name, &spaceGuard{src: stdin, free: free}); err != nil {
		if errors.Is(err, errNoSpace) {
			_ = vol.Delete(cmd.name)
			fmt.Fprintf(stderr, "relist-volhelper: write %q exceeded the Volume's free space (only %d bytes free when it began)\n", cmd.name, free)
			return exitRefused
		}
		return fail(stderr, err)
	}
	return exitOK
}

// runExtract is extract's dispatch: the client's tar.gz streams from stdin
// into the target through volfs.Extract with the spec's default restore caps
// (10k entries, 4 GiB uncompressed; the 1 GiB compressed cap belongs to the
// HTTP layer, on the request body). A refusal — an unsafe entry, a cap
// exceeded, or a body that is not a usable tar.gz — is the client's input's
// fault, not the Volume's, so it exits 4 for the panel to answer with a 4xx;
// an I/O, target or space failure stays exit 1. The message names the
// offending entry where one is known.
func runExtract(stderr io.Writer, vol *volfs.FS, stdin io.Reader, cmd command) int {
	err := vol.Extract(cmd.name, stdin)
	if err == nil {
		return exitOK
	}
	if errors.Is(err, volfs.ErrUnsafeArchive) ||
		errors.Is(err, volfs.ErrArchiveTooLarge) ||
		errors.Is(err, volfs.ErrInvalidArchive) {
		fmt.Fprintf(stderr, "relist-volhelper: %v\n", err)
		return exitArchiveRefused
	}
	return fail(stderr, err)
}

// spaceGuard wraps a write's source and stops it delivering more bytes than
// the free space measured when the write began. It is a budget check, not a
// reservation — another writer can still take the space — and the budget can
// be consumed to the last byte: a stream that ends exactly at the limit is
// not an overflow.
type spaceGuard struct {
	src     io.Reader
	free    uint64
	written uint64
}

// Read delivers at most the bytes left in the budget. At the limit it probes
// the source with a single byte so an exactly-sized stream can end in EOF
// while one more byte is refused as the overflow it would be.
func (g *spaceGuard) Read(p []byte) (int, error) {
	rem := g.free - g.written
	if rem == 0 {
		var probe [1]byte
		n, err := g.src.Read(probe[:])
		if n > 0 {
			return 0, errNoSpace
		}
		return 0, err
	}
	if rem < uint64(len(p)) {
		p = p[:rem]
	}
	n, err := g.src.Read(p)
	g.written += uint64(n)
	return n, err
}

// runNewEntry is mkdir's and create's dispatch: a name already taken is the
// one expected refusal of a create — mkdir merges nothing and create never
// overwrites — so it exits 5, which the panel answers 409 Conflict rather
// than 502. Every other failure is an ordinary operation failure.
func runNewEntry(stderr io.Writer, err error) int {
	if errors.Is(err, fs.ErrExist) {
		fmt.Fprintf(stderr, "relist-volhelper: %v\n", err)
		return exitExists
	}
	return failOn(stderr, err)
}

// runProbe tells a missing path from an empty directory and from anything
// holding data, a file of zero bytes included, so a Migration never takes
// an occupied path for a free one.
func runProbe(stdout, stderr io.Writer, vol *volfs.FS, name string) int {
	var answer struct {
		Exists bool `json:"exists"`
		Empty  bool `json:"empty"`
	}
	entry, err := vol.Stat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return fail(stderr, err)
	default:
		answer.Exists = true
		if entry.Kind == volfs.KindDir {
			children, err := vol.List(name)
			if err != nil {
				return fail(stderr, err)
			}
			answer.Empty = len(children) == 0
		}
	}
	if err := json.NewEncoder(stdout).Encode(answer); err != nil {
		return fail(stderr, fmt.Errorf("write probe: %w", err))
	}
	return exitOK
}

// writeBytes answers size and free with {"bytes":N}.
func writeBytes(stdout, stderr io.Writer, n int64) int {
	if err := json.NewEncoder(stdout).Encode(struct {
		Bytes int64 `json:"bytes"`
	}{n}); err != nil {
		return fail(stderr, fmt.Errorf("write bytes: %w", err))
	}
	return exitOK
}

// fail reports one operation's error on stderr — the only place errors go,
// so stdout stays exactly the operation's bytes or JSON — and returns the
// failure code.
func fail(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "relist-volhelper: %v\n", err)
	return exitError
}

// failOn is fail for an operation with no output of its own.
func failOn(stderr io.Writer, err error) int {
	if err != nil {
		return fail(stderr, err)
	}
	return exitOK
}
