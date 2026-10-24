// Package volumes owns the Volume file operations of ADR-0010: one helper
// container run per operation, driven over argv and the container's
// stdin/stdout; the resolution of which Volumes a Compose mounts and what
// uid/gid an operation runs as; and the pre-flight that decides whether a
// source may be mounted at all. It declares both of its ports — in the
// consumer, per ADR-0003 — the Runner, implemented for real in the leaf
// package `dockerexec` and faked in tests by `volumes/faketest`, and the CLI
// that resolution and pre-flight speak to Docker through.
package volumes

import (
	"context"
	"io"
)

// Op is one helper operation, in the helper's own wire vocabulary: the exact
// argv element the container's entrypoint receives.
type Op string

// The v1 operation set (the spec's Protocol). The mount is read-only for
// list, stat, read and archive, and read-write for write, delete, extract,
// mkdir and create; Request.ReadOnly carries that choice into the mount flag,
// where the kernel enforces it.
const (
	OpList    Op = "list"
	OpStat    Op = "stat"
	OpRead    Op = "read"
	OpWrite   Op = "write"
	OpDelete  Op = "delete"
	OpArchive Op = "archive"
	OpExtract Op = "extract"
	OpMkdir   Op = "mkdir"
	OpCreate  Op = "create"
)

// Request is one fully resolved Volume operation: everything a Runner needs
// to run the helper against one Volume. Source is the resolved mount source —
// a named volume's real name or a bind mount's absolute host path — and it is
// the only thing ever mounted (ADR-0010). User is the explicit `--user` value
// (`<uid>:<gid>`), root included only as a deliberate opt-in. Path is relative
// to the Volume root, which the helper sees as `/data`. DeclaredBytes is an
// upload's declared size, when the endpoint knows it, passed to `write` as
// `--expect <n>` so the helper can refuse a write that cannot fit before it
// streams; zero means no declared size. ReadOnly is the read/write choice:
// true mounts `:ro`, and the kernel is the enforcer.
type Request struct {
	Source        string
	User          string
	Op            Op
	Path          string
	DeclaredBytes int64
	// After is a list's name cursor: only names sorting after it are listed.
	After    string
	ReadOnly bool
}

// RefusalError is a helper refusal: the helper exited 3 before doing the
// work — a declared upload that cannot fit the Volume's free space, or a
// stream that overran the free space measured when it began — and Message is
// the helper's own explanation, naming need against available. Every other
// non-zero exit is an ordinary failure; this type exists so the HTTP layer
// can answer 507 Insufficient Storage with the helper's message instead of
// the 502 every other run failure gets.
type RefusalError struct {
	Message string
}

// Error implements error. A refusal that somehow carried no message still
// reads as a refusal rather than as an empty failure.
func (e *RefusalError) Error() string {
	if e.Message == "" {
		return "the helper refused the operation"
	}
	return e.Message
}

// ArchiveRefusedError is a helper refusal of the bytes it was fed: the helper
// exited 4 because the stream was not a usable tar.gz, named an entry unsafe
// to write, or ran past one of the restore caps, and Message is the helper's
// own explanation, naming the offending entry where one is known. The body is
// the client's, so this type exists so the HTTP layer can answer 422
// Unprocessable Content with the helper's message instead of the 502 every
// other run failure gets — or the 507 a space refusal gets.
type ArchiveRefusedError struct {
	Message string
}

// Error implements error. A refusal that somehow carried no message still
// reads as an archive refusal rather than as an empty failure.
func (e *ArchiveRefusedError) Error() string {
	if e.Message == "" {
		return "the helper refused the archive"
	}
	return e.Message
}

// ExistsError is a helper refusal of a create: the helper exited 5 because the
// name it was asked to create is already taken — mkdir and create never merge
// or overwrite — and Message is the helper's own explanation. It has its own
// type so the HTTP layer can answer 409 Conflict with the truth instead of the
// 502 every other run failure gets.
type ExistsError struct {
	Message string
}

// Error implements error. A collision that somehow carried no message still
// reads as a collision rather than as an empty failure.
func (e *ExistsError) Error() string {
	if e.Message == "" {
		return "the name already exists"
	}
	return e.Message
}

// The helper's exit codes for the typed refusals above — 3 for a refusal
// before any work, 4 for a refused archive, 5 for a name already taken. They
// live here, next to the error types the runner maps them to, so the helper
// that exits with them and the runner that reads them cannot skew.
const (
	RefusalExitCode        = 3
	ArchiveRefusedExitCode = 4
	ExistsExitCode         = 5
)

// Runner is the seam that turns one operation into one helper run. Run
// streams the operation's bytes over stdin/stdout — write and extract consume
// stdin, read and archive produce stdout — and returns an error with the
// helper's stderr folded in. Nothing is staged on either side of it.
//
// Available reports whether the feature can work at all: the helper runs out
// of the panel's own image, so a panel running as a bare binary or without
// the Docker socket must say so — with a message the UI can show — before any
// screen offers the Explorer.
type Runner interface {
	Available(ctx context.Context) error
	Run(ctx context.Context, req Request, stdin io.Reader, stdout io.Writer) error
}
