package dockerexec

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"relist-panel/internal/databases"
)

// demuxStream copies Docker's multiplexed stream (a non-TTY exec) to stdout
// and stderr. Each frame is an 8-byte header, [stream, 0, 0, 0, size uint32
// BE], then size bytes. A nil writer discards its stream. A clean end between
// frames is nil; a stream cut inside a frame is an error.
func demuxStream(src io.Reader, stdout, stderr io.Writer) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	var header [8]byte
	for {
		if _, err := io.ReadFull(src, header[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("docker exec stream: %w", err)
		}
		size := int64(binary.BigEndian.Uint32(header[4:]))
		dst := stdout
		if header[0] == 2 {
			dst = stderr
		}
		if n, err := io.CopyN(dst, src, size); err != nil {
			if n < size && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) {
				return fmt.Errorf("docker exec stream: %w", io.ErrUnexpectedEOF)
			}
			return err
		}
	}
}

// readErrReader remembers an error from the wrapped reader, so a failed read
// of stdin is told apart from a failed write to the closed connection.
type readErrReader struct {
	r   io.Reader
	err error
}

func (r *readErrReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.err = err
	}
	return n, err
}

// Exec runs req.Cmd in the container with no TTY and streams stdin and output
// over the daemon's hijacked exec connection.
func (h *DatabaseHost) Exec(ctx context.Context, name string, req databases.ExecRequest) (int, error) {
	createPath := "/containers/" + url.PathEscape(name) + "/exec"
	resp, body, err := h.exec.send(ctx, http.MethodPost, createPath, createExecRequest{
		Cmd:          req.Cmd,
		Tty:          false,
		AttachStdin:  req.Stdin != nil,
		AttachStdout: true,
		AttachStderr: true,
		Env:          req.Env,
	})
	if err != nil {
		return 0, err
	}
	if !ok(resp.StatusCode) {
		// An *engineError keeps the status, so a caller can tell a missing
		// container (404) from a stopped one (409).
		return 0, &engineError{status: resp.StatusCode, msg: apiError(createPath, resp.Status, body).Error()}
	}
	execID, err := createdExecID(body)
	if err != nil {
		return 0, fmt.Errorf("docker api %s: decode: %w", createPath, err)
	}

	conn, reader, err := hijackSocket(ctx, h.socketPath, "/exec/"+url.PathEscape(execID)+"/start", startExecRequest{Detach: false, Tty: false})
	if err != nil {
		return 0, err
	}
	// A cancelled ctx closes the connection, which ends the copy below.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer conn.Close()

	var stdinErr error
	stdinDone := make(chan struct{})
	if req.Stdin != nil {
		src := &readErrReader{r: req.Stdin}
		go func() {
			defer close(stdinDone)
			// A write error here means the command stopped reading (it
			// exited early); its exit code says why.
			_, _ = io.Copy(conn, src)
			if uc, ok := conn.(*net.UnixConn); ok {
				_ = uc.CloseWrite()
			}
			stdinErr = src.err
		}()
	} else {
		close(stdinDone)
	}

	demuxErr := demuxStream(reader, req.Stdout, req.Stderr)
	_ = conn.Close()
	<-stdinDone
	if ctx.Err() != nil {
		return 0, fmt.Errorf("docker exec %s: %w", name, ctx.Err())
	}
	if demuxErr != nil {
		return 0, demuxErr
	}
	if stdinErr != nil {
		return 0, fmt.Errorf("docker exec %s: read stdin: %w", name, stdinErr)
	}
	return h.exec.waitExec(ctx, execID)
}
