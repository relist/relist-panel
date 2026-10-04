package volumeapi

import (
	"context"
	"net/http"
	"path/filepath"
	"time"

	"relist-panel/internal/volumes"
)

// archiveTimeout caps one archive stream: the spec's ten-minute cap, applied
// to the run's context so the deadline reaches the docker CLI child and the
// helper follows it down (ADR-0010). It is a package variable so a test can
// shrink it.
var archiveTimeout = 10 * time.Minute

// archive streams one backup: the helper's tar.gz straight from its stdout
// into the response, byte for byte, with nothing staged anywhere (the spec's
// Archives) and the stream's duration capped.
func (s *Service) archive(w http.ResponseWriter, r *http.Request) {
	req, volume, ok := s.operation(w, r, volumes.OpArchive)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), archiveTimeout)
	defer cancel()

	s.stream(w, ctx, req, "application/gzip", contentDisposition(archiveName(volume, req.Path)))
}

// archiveName names a tar.gz download: after the Volume — its logical name,
// or the base of a bind's resolved source — when the whole Volume is
// archived, and after the base of the archived path otherwise. The result
// still passes through contentDisposition, which strips anything a header
// cannot carry.
func archiveName(volume volumes.Volume, path string) string {
	if path == "." {
		if volume.LogicalName != "" {
			return volume.LogicalName + ".tar.gz"
		}
		return filepath.Base(volume.Source) + ".tar.gz"
	}
	return filepath.Base(path) + ".tar.gz"
}
