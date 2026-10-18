package deployments

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"relist-panel/internal/composes"
	"relist-panel/internal/sources"
)

func TestSecretRedactionMasksLongestOverlappingValue(t *testing.T) {
	var out strings.Builder
	w := newRedactingWriter(&out, &executionSnapshot{Files: &composes.ExecutionSnapshot{Secrets: []string{"pass", "password", "pass"}}})
	_, _ = w.Write([]byte("password"))
	_ = w.flush()
	if out.String() != "[redacted]" {
		t.Fatalf("longer secret suffix exposed:%q", out.String())
	}
}

func TestSecretRedactionMasksSplitOverlappingValues(t *testing.T) {
	for _, chunks := range [][]string{{"pass", "word"}, {"before pass", "word after"}, {"before pa", "sswo", "rd after"}} {
		var out strings.Builder
		w := newRedactingWriter(&out, &executionSnapshot{Files: &composes.ExecutionSnapshot{Secrets: []string{"pass", "password"}}})
		for _, chunk := range chunks {
			if n, err := w.Write([]byte(chunk)); err != nil || n != len(chunk) {
				t.Fatalf("Write = %d, %v", n, err)
			}
		}
		if err := w.flush(); err != nil {
			t.Fatal(err)
		}
		want := "[redacted]"
		if strings.HasPrefix(chunks[0], "before ") {
			want = "before [redacted] after"
		}
		if got := out.String(); got != want {
			t.Fatalf("chunks %q produced %q, want %q", chunks, got, want)
		}
	}
}

func TestSecretRedactionKeepsOverlappingSecretSuffixMasked(t *testing.T) {
	var out strings.Builder
	w := newRedactingWriter(&out, &executionSnapshot{Files: &composes.ExecutionSnapshot{Secrets: []string{"abcdef", "defghi"}}})
	for _, chunk := range []string{"abcdefg", "hi done"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.flush(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "[redacted] done" {
		t.Fatalf("overlapping secrets exposed a suffix: %q", got)
	}
}

func TestSecretRedactionDoesNotRedactItsReplacementMarker(t *testing.T) {
	var out strings.Builder
	w := newRedactingWriter(&out, &executionSnapshot{Files: &composes.ExecutionSnapshot{Secrets: []string{"red", "password"}}})
	for _, chunk := range []string{"password", " tail"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.flush(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "[redacted] tail" {
		t.Fatalf("replacement marker was processed as raw log data: %q", got)
	}
	if got := w.redact("error: password"); got != "error: [redacted]" {
		t.Fatalf("metadata redaction = %q", got)
	}
}

func TestSecretRedactionMasksMultilineKeyAcrossWrites(t *testing.T) {
	key := "-----BEGIN KEY-----\nprivate-body\n-----END KEY-----"
	var out strings.Builder
	w := newRedactingWriter(&out, &executionSnapshot{Source: &sources.Source{PrivateKey: key}})
	for _, chunk := range []string{"cli ", "-----BEGIN KEY-----\nprivate-", "body\n-----END KEY-----", "\nfinished\n"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.flush(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "cli [redacted]\nfinished\n" {
		t.Fatalf("multiline key redaction = %q", got)
	}
}

func TestSecretRedactionStreamsPlainOutput(t *testing.T) {
	for _, snapshot := range []*executionSnapshot{nil, {Files: &composes.ExecutionSnapshot{Secrets: []string{"password"}}}} {
		var out strings.Builder
		w := newRedactingWriter(&out, snapshot)
		text := strings.Repeat("ordinary output\n", 100)
		if n, err := w.Write([]byte(text)); n != len(text) || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
		if out.Len() == 0 {
			t.Fatal("plain output remained buffered until completion")
		}
		if err := w.flush(); err != nil {
			t.Fatal(err)
		}
		if err := w.flush(); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); got != text {
			t.Fatalf("plain output changed: %q", got)
		}
	}
}

func TestSecretRedactionSerializesConcurrentWrites(t *testing.T) {
	var out bytes.Buffer
	w := newRedactingWriter(&out, &executionSnapshot{Files: &composes.ExecutionSnapshot{Secrets: []string{"private-token"}}})
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 25 {
				if _, err := w.Write([]byte("private-token\n")); err != nil {
					t.Error(err)
				}
			}
		})
	}
	workers.Wait()
	if err := w.flush(); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), strings.Repeat("[redacted]\n", 200); got != want {
		t.Fatalf("concurrent log output was lost, interleaved or exposed: %q", got)
	}
}

func TestSecretRedactionReturnsOutputErrors(t *testing.T) {
	failure := errors.New("log disk unavailable")
	w := newRedactingWriter(redactionFailWriter{err: failure}, nil)
	if n, err := w.Write([]byte("ordinary output")); n != 0 || !errors.Is(err, failure) {
		t.Fatalf("Write = %d, %v, want 0 and original output error", n, err)
	}
	if err := w.flush(); !errors.Is(err, failure) {
		t.Fatalf("flush after failed output = %v, want original output error", err)
	}
}

func TestSecretRedactionReturnsFlushError(t *testing.T) {
	failure := errors.New("log disk unavailable")
	w := newRedactingWriter(redactionFailWriter{err: failure}, &executionSnapshot{Files: &composes.ExecutionSnapshot{Secrets: []string{"password"}}})
	if n, err := w.Write([]byte("pass")); n != 4 || err != nil {
		t.Fatalf("buffered Write = %d, %v", n, err)
	}
	if err := w.flush(); !errors.Is(err, failure) {
		t.Fatalf("flush = %v, want original output error", err)
	}
}

func TestSecretRedactionRejectsShortOutputWrite(t *testing.T) {
	w := newRedactingWriter(redactionFailWriter{short: true}, nil)
	if n, err := w.Write([]byte("ordinary output")); n != 0 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short Write = %d, %v, want 0 and io.ErrShortWrite", n, err)
	}
}

type redactionFailWriter struct {
	err   error
	short bool
}

func (w redactionFailWriter) Write(p []byte) (int, error) {
	if w.short {
		return len(p) / 2, nil
	}
	return 0, w.err
}
