package volumeapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"relist-panel/internal/volumes"
)

// uploadCap is the spec's hard-coded upload limit: 1 GiB, no env knob. The
// declared Content-Length is checked against it before any helper run; the
// Volume's own free space is the helper's check, so a size the panel accepts
// can still be refused when it cannot fit (the spec's Caps).
const uploadCap = 1 << 30

// writeFile serves an upload or an edit: the request body is the file's
// bytes, streamed straight into the run's stdin — never staged, never
// buffered. The declared Content-Length is required, because it becomes
// DeclaredBytes: the helper runs `write --expect <n>` and refuses a size that
// cannot fit the Volume before it reads any stdin (exit 3, answered 507).
// The helper's own stream guard catches an overrunning body the same way; its
// clean-up — it truncates the target, so an aborted write removes the partial
// file best-effort — is the helper's behaviour, and this handler only reports
// the message.
func (s *Service) writeFile(w http.ResponseWriter, r *http.Request) {
	req, _, ok := s.contentOperation(w, r, volumes.OpWrite)
	if !ok {
		return
	}

	if r.ContentLength < 0 {
		// A chunked body has no declared size, so `--expect` has nothing to
		// carry. The panel's own UI always sends Content-Length.
		writeError(w, http.StatusLengthRequired, "upload requires a declared length (Content-Length)")
		return
	}
	if r.ContentLength > uploadCap {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("upload exceeds the %d GiB cap", uploadCap>>30))
		return
	}
	req.DeclaredBytes = r.ContentLength

	if err := s.runner.Run(r.Context(), req, r.Body, io.Discard); err != nil {
		writeRunFailure(w, req.Op, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteFile serves one permanent delete: a file or a whole directory tree,
// no trash (the spec's v1 operation set). The path rule has already refused
// the Volume root; the run mounts read-write, consumes no stdin and produces
// no output.
func (s *Service) deleteFile(w http.ResponseWriter, r *http.Request) {
	req, _, ok := s.contentOperation(w, r, volumes.OpDelete)
	if !ok {
		return
	}

	if err := s.runner.Run(r.Context(), req, nil, io.Discard); err != nil {
		writeRunFailure(w, req.Op, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeRunFailure answers a failed write or delete: a helper refusal — exit
// 3, so the operation was not carried out — is 507 Insufficient Storage
// carrying the helper's own message; every other failure stays the 502 the
// read operations use.
func writeRunFailure(w http.ResponseWriter, op volumes.Op, err error) {
	var refusal *volumes.RefusalError
	if errors.As(err, &refusal) {
		writeError(w, http.StatusInsufficientStorage, "volume "+string(op)+" refused: "+refusal.Error())
		return
	}
	writeError(w, http.StatusBadGateway, "volume "+string(op)+" failed: "+err.Error())
}
