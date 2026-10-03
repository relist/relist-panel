package volumeapi

import (
	"io"
	"net/http"

	"relist-panel/internal/volumes"
)

// mkdir creates one directory — the Explorer's New folder — as exactly one
// helper run: read-write, no stdin and no output. An existing name is the
// helper's exit 5, answered 409 rather than 502.
func (s *Service) mkdir(w http.ResponseWriter, r *http.Request) {
	s.createEntry(w, r, volumes.OpMkdir)
}

// createFile creates one empty file — the Explorer's New file — as exactly
// one helper run. It is deliberately not the write endpoint: create refuses an
// existing name instead of truncating it, so the dialog can never silently
// wipe the file it collided with.
func (s *Service) createFile(w http.ResponseWriter, r *http.Request) {
	s.createEntry(w, r, volumes.OpCreate)
}

// createEntry is mkdir's and createFile's one pipeline: the write family's
// validation — read-write mount, a path that names an entry rather than the
// Volume root refused as a 400 before any run, and availability, membership,
// pre-flight and user acknowledgement unchanged from every other operation —
// then one consuming-nothing, producing-nothing run. Success is 204.
func (s *Service) createEntry(w http.ResponseWriter, r *http.Request, op volumes.Op) {
	req, _, ok := s.contentOperation(w, r, op)
	if !ok {
		return
	}

	if err := s.runner.Run(r.Context(), req, nil, io.Discard); err != nil {
		writeRunFailure(w, req.Op, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
