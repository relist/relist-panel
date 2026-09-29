package volumeapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"relist-panel/internal/volumes"
)

// restoreCap is the spec's hard-coded compressed restore cap: 1 GiB on the
// request body, no env knob. The uncompressed and entry caps are the helper's,
// counted as it streams, and arrive as an archive refusal (exit 4 → 422); the
// panel never reads the body to enforce its own.
const restoreCap = 1 << 30

// restore serves a streamed tar.gz extraction: the request body is the
// archive's bytes, streamed straight into the run's stdin — nothing is staged
// anywhere (the spec's Restore). The path names the target directory, which
// must already exist; unlike the write family's path, the Volume root
// (absent, empty or ".") is legal because extract merges into a directory.
// The run mounts read-write and consumes stdin.
func (s *Service) restore(w http.ResponseWriter, r *http.Request) {
	req, _, ok := s.operationOf(w, r, volumes.OpExtract, false, operationPath)
	if !ok {
		return
	}

	if r.ContentLength < 0 {
		// A chunked body has no declared size to check against the
		// compressed cap. The panel's own UI always sends Content-Length.
		writeError(w, http.StatusLengthRequired, "restore requires a declared length (Content-Length)")
		return
	}
	if r.ContentLength > restoreCap {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("restore exceeds the %d GiB compressed cap", restoreCap>>30))
		return
	}

	if err := s.runner.Run(r.Context(), req, r.Body, io.Discard); err != nil {
		restoreFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// restoreFailure answers a failed extract: an archive refusal — exit 4, so
// the body was unsafe, oversized or not a usable tar.gz — is 422
// Unprocessable Content carrying the helper's own message, naming the
// offending entry where one is known; everything else maps exactly as the
// write family does, a space refusal (exit 3) to 507 Insufficient Storage and
// every other failure to 502.
func restoreFailure(w http.ResponseWriter, err error) {
	var refusal *volumes.ArchiveRefusedError
	if errors.As(err, &refusal) {
		writeError(w, http.StatusUnprocessableEntity, "volume extract refused: "+refusal.Error())
		return
	}
	writeRunFailure(w, volumes.OpExtract, err)
}
