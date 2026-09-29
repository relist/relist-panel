// Package volumes owns the Volume file operations of ADR-0010: one helper
// container run per operation, driven over argv and the container's
// stdin/stdout. It declares the Runner port — in the consumer, per ADR-0003 —
// implemented for real in the leaf package `dockerexec` and faked in tests by
// `volumes/faketest`. Resolving which source a Volume is, and the uid/gid
// pre-flight, land with the endpoints that need them.
package volumes

import (
	"context"
	"io"
)

// Op is one helper operation, in the helper's own wire vocabulary: the exact
// argv element the container's entrypoint receives.
type Op string

// The v1 operation set (the spec's Protocol). The mount is read-only for
// list, stat, read and archive, and read-write for write, delete and extract;
// Request.ReadOnly carries that choice into the mount flag, where the kernel
// enforces it.
const (
	OpList    Op = "list"
	OpStat    Op = "stat"
	OpRead    Op = "read"
	OpWrite   Op = "write"
	OpDelete  Op = "delete"
	OpArchive Op = "archive"
	OpExtract Op = "extract"
)

// Request is one fully resolved Volume operation: everything a Runner needs
// to run the helper against one Volume. Source is the resolved mount source —
// a named volume's real name or a bind mount's absolute host path — and it is
// the only thing ever mounted (ADR-0010). User is the explicit `--user` value
// (`<uid>:<gid>`), root included only as a deliberate opt-in. Path is relative
// to the Volume root, which the helper sees as `/data`. ReadOnly is the
// read/write choice: true mounts `:ro`, and the kernel is the enforcer.
type Request struct {
	Source   string
	User     string
	Op       Op
	Path     string
	ReadOnly bool
}

// Runner is the seam that turns one operation into one helper run. Run
// streams the operation's bytes over stdin/stdout — write and extract consume
// stdin, read and archive produce stdout — and returns an error with the
// helper's stderr folded in. Nothing is staged on either side of it.
type Runner interface {
	Run(ctx context.Context, req Request, stdin io.Reader, stdout io.Writer) error
}
