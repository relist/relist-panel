package volumeapi

import (
	"bytes"
	"net/http"
	"testing"
)

// recordingWriter is a bare http.ResponseWriter that records status codes and
// body bytes, so a deferred WriteHeader is observable.
type recordingWriter struct {
	header   http.Header
	statuses []int
	body     bytes.Buffer
}

func (r *recordingWriter) Header() http.Header         { return r.header }
func (r *recordingWriter) Write(p []byte) (int, error) { return r.body.Write(p) }
func (r *recordingWriter) WriteHeader(status int)      { r.statuses = append(r.statuses, status) }

// The lazy status is what lets a read or archive that fails before its first
// byte answer 502 instead of a committed 200: neither the status nor the
// download headers are written until a byte arrives.
func TestStatusWriterDefersTheStatusAndHeadersUntilTheFirstByte(t *testing.T) {
	recorder := &recordingWriter{header: http.Header{}}
	w := &statusWriter{w: recorder, headers: http.Header{
		"Content-Type":        []string{"application/gzip"},
		"Content-Disposition": []string{`attachment; filename="pgdata.tar.gz"`},
	}}

	if _, err := w.Write(nil); err != nil {
		t.Fatalf("empty write: %v", err)
	}
	if w.wrote {
		t.Error("an empty write committed the status, want it still pending")
	}
	if len(recorder.statuses) != 0 {
		t.Errorf("statuses = %v, want none before the first byte", recorder.statuses)
	}
	if got := recorder.Header().Get("Content-Disposition"); got != "" {
		t.Errorf("Content-Disposition = %q before the first byte, want none", got)
	}

	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatalf("first byte write: %v", err)
	}
	if !w.wrote {
		t.Error("wrote = false after the first byte, want true")
	}
	if len(recorder.statuses) != 1 || recorder.statuses[0] != http.StatusOK {
		t.Errorf("statuses = %v, want exactly [200]", recorder.statuses)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/gzip" {
		t.Errorf("Content-Type = %q, want the held application/gzip", got)
	}
	if got := recorder.Header().Get("Content-Disposition"); got != `attachment; filename="pgdata.tar.gz"` {
		t.Errorf("Content-Disposition = %q, want the held disposition", got)
	}

	if _, err := w.Write([]byte(" again")); err != nil {
		t.Fatalf("second write: %v", err)
	}
	if len(recorder.statuses) != 1 {
		t.Errorf("statuses = %v, want the status written once", recorder.statuses)
	}
	if got := recorder.body.String(); got != "hello again" {
		t.Errorf("body = %q, want %q", got, "hello again")
	}
}

// A successful run that produced no bytes still commits 200 with its headers,
// and commit never writes the status twice.
func TestStatusWriterCommitAnswersAnEmptyRunOnce(t *testing.T) {
	recorder := &recordingWriter{header: http.Header{}}
	w := &statusWriter{w: recorder, headers: http.Header{
		"Content-Disposition": []string{`attachment; filename="pgdata.tar.gz"`},
	}}

	w.commit()
	w.commit()

	if len(recorder.statuses) != 1 || recorder.statuses[0] != http.StatusOK {
		t.Errorf("statuses = %v, want exactly [200]", recorder.statuses)
	}
	if got := recorder.Header().Get("Content-Disposition"); got != `attachment; filename="pgdata.tar.gz"` {
		t.Errorf("Content-Disposition = %q, want the held disposition", got)
	}
}
