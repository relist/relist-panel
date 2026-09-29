// Package faketest provides the test doubles for the volumes package's ports
// that the spec's Testing Decisions call for: HTTP-seam tests install them in
// place of the real `docker run --rm` adapter and the real Docker CLI, script
// their answers, and assert exactly what would have run — all with no Docker
// in them. It lives in its own package so nothing but tests imports it.
package faketest

import (
	"context"
	"io"
	"time"

	"relist-panel/internal/volumes"
)

// Call is one Run the fake served: the Request as it was passed (source,
// user, op, path, read-only), the deadline of the context the Run was given —
// zero means the context was unbounded — and the bytes drained from stdin.
// Stdout is not recorded — tests script it.
type Call struct {
	Request  volumes.Request
	Deadline time.Time
	Stdin    []byte
}

// Runner is a scripted volumes.Runner. Its zero value records calls and
// answers with an empty stdout, no error and the feature available.
type Runner struct {
	// Calls records every Run, in order.
	Calls []Call
	// Stdout is written to the caller's writer on every Run, and Err
	// returned after it. Tests mutate both between calls to script a
	// sequence.
	Stdout string
	Err    error
	// AvailableErr is returned by Available; the zero value — nil — answers
	// that the feature is available.
	AvailableErr error
}

// Available implements volumes.Runner.
func (f *Runner) Available(_ context.Context) error {
	return f.AvailableErr
}

// Run implements volumes.Runner.
func (f *Runner) Run(ctx context.Context, req volumes.Request, stdin io.Reader, stdout io.Writer) error {
	call := Call{Request: req}
	if deadline, ok := ctx.Deadline(); ok {
		call.Deadline = deadline
	}
	if stdin != nil {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		call.Stdin = data
	}
	f.Calls = append(f.Calls, call)

	if f.Stdout != "" && stdout != nil {
		if _, err := io.WriteString(stdout, f.Stdout); err != nil {
			return err
		}
	}
	return f.Err
}

var _ volumes.Runner = (*Runner)(nil)
