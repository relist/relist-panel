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

// The lazy status is what lets a read that fails before its first byte answer
// 502 instead of a committed 200: nothing is written until a byte arrives,
// and the status is written exactly once.
func TestStatusWriterDefersTheStatusUntilTheFirstByte(t *testing.T) {
	recorder := &recordingWriter{header: http.Header{}}
	w := &statusWriter{w: recorder}

	if _, err := w.Write(nil); err != nil {
		t.Fatalf("empty write: %v", err)
	}
	if w.wrote {
		t.Error("an empty write committed the status, want it still pending")
	}
	if len(recorder.statuses) != 0 {
		t.Errorf("statuses = %v, want none before the first byte", recorder.statuses)
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
