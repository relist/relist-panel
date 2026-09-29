// Command relist-volhelper is the Volume helper container's entire program:
// one run per operation, argv in and bytes over stdin/stdout, no server, no
// socket, no network (ADR-0010, the spec's Protocol). The panel's runner
// invokes it out of the panel's own image with exactly one Volume mounted at
// /data (the spec's Run spec):
//
//	relist-volhelper list    -- <path>
//	relist-volhelper stat    -- <path>
//	relist-volhelper read    -- <path>
//	relist-volhelper write   -- <path>
//	relist-volhelper delete  -- <path>
//	relist-volhelper archive --format tar.gz -- <path>
//
// Paths are relative to /data and are only ever read after `--`, so a name
// starting with `-` cannot be taken for a flag. list and stat answer with
// JSON on stdout, read and archive with raw bytes, and write consumes stdin;
// every failure goes to stderr with a non-zero exit. All the dispatch lives
// in run, below the process boundary, so the whole protocol is tested without
// a container.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"relist-panel/internal/volfs"
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
  relist-volhelper write   -- <path>
  relist-volhelper delete  -- <path>
  relist-volhelper archive --format tar.gz -- <path>
<path> is relative to the Volume root mounted at /data.
`

// The helper's exit codes: 0 for success, 1 when an operation failed, 2 when
// the invocation itself was refused. Any non-zero code is a failure to the
// panel; the split only sharpens the helper's own diagnostics.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

func main() {
	// No signal handler, on purpose. As pid1 the helper must keep Go's
	// default SIGTERM behaviour — die immediately — so a stop stays fast:
	// measured at ~1.1 s, against ~10.1 s when pid1 traps or ignores SIGTERM
	// (the spec's execution costs). Nothing here calls signal.Notify.
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, dataDir))
}

// run is the helper's whole protocol: parse one argv, refuse an absolute path
// before any filesystem call, open the Volume root once, and dispatch exactly
// one operation. It returns the process exit code.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, rootDir string) int {
	op, name, err := parse(args)
	if err != nil {
		fmt.Fprintf(stderr, "relist-volhelper: %v\n\n%s", err, usage)
		return exitUsage
	}

	// Absolute paths are refused here, before os.OpenRoot, so the refusal
	// never depends on the mount existing or on os.Root having to catch it.
	// os.Root would refuse the path too — defense in depth, not the guard.
	if filepath.IsAbs(name) {
		fmt.Fprintf(stderr, "relist-volhelper: absolute path %q is not allowed: paths are relative to the Volume root\n", name)
		return exitUsage
	}

	vol, err := volfs.Open(rootDir)
	if err != nil {
		fmt.Fprintf(stderr, "relist-volhelper: %v\n", err)
		return exitError
	}
	defer vol.Close()

	switch op {
	case "list":
		entries, err := vol.List(name)
		if err != nil {
			return fail(stderr, err)
		}
		if err := json.NewEncoder(stdout).Encode(entries); err != nil {
			return fail(stderr, fmt.Errorf("write listing: %w", err))
		}
		return exitOK
	case "stat":
		entry, err := vol.Stat(name)
		if err != nil {
			return fail(stderr, err)
		}
		if err := json.NewEncoder(stdout).Encode(entry); err != nil {
			return fail(stderr, fmt.Errorf("write stat: %w", err))
		}
		return exitOK
	case "read":
		return failOn(stderr, vol.Read(name, stdout))
	case "write":
		return failOn(stderr, vol.Write(name, stdin))
	case "delete":
		return failOn(stderr, vol.Delete(name))
	case "archive":
		// parse pinned the format: tar.gz is the only one the helper speaks.
		return failOn(stderr, vol.Archive(name, stdout))
	default:
		// Unreachable while parse and this switch agree; a failure here is a
		// bug, and must still be a non-zero exit, not a silent success.
		fmt.Fprintf(stderr, "relist-volhelper: unhandled operation %q\n\n%s", op, usage)
		return exitUsage
	}
}

// parse accepts exactly one argv shape per op: `-- <path>` for every op, and
// `--format tar.gz -- <path>` for archive. `--` is mandatory, so a name
// starting with `-` is always a path and never a flag; the flag order is
// fixed because the panel is the only caller and emits precisely this shape.
func parse(args []string) (op, name string, err error) {
	if len(args) == 0 {
		return "", "", errors.New("missing operation")
	}
	op, rest := args[0], args[1:]

	switch op {
	case "list", "stat", "read", "write", "delete":
		// No extra flags: the path follows immediately after `--`.
	case "archive":
		if len(rest) < 2 || rest[0] != "--format" {
			return "", "", errors.New("archive requires --format tar.gz")
		}
		if rest[1] != "tar.gz" {
			return "", "", fmt.Errorf("unsupported archive format %q: the only format is tar.gz", rest[1])
		}
		rest = rest[2:]
	default:
		return "", "", fmt.Errorf("unknown operation %q", op)
	}

	if len(rest) != 2 || rest[0] != "--" {
		return "", "", fmt.Errorf("%s takes `-- <path>`", op)
	}
	return op, rest[1], nil
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
