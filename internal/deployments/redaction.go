package deployments

import (
	"io"
	"slices"
	"strings"
	"sync"
)

// redactingWriter retains a suffix to recognize secrets split over CLI writes.
// This masks known snapshot secrets, rather than dumping private input to logs.
type redactingWriter struct {
	mu      sync.Mutex
	out     io.Writer
	pending string
	covered []bool
	masked  bool
	secrets []string
	hold    int
	err     error
}

func newRedactingWriter(out io.Writer, snapshot *executionSnapshot) *redactingWriter {
	w := &redactingWriter{out: out}
	if snapshot == nil {
		return w
	}
	if snapshot.Files != nil {
		w.secrets = append(w.secrets, snapshot.Files.Secrets...)
	}
	if snapshot.Source != nil {
		w.secrets = append(w.secrets, snapshot.Source.PrivateKey)
	}
	for _, secret := range append([]string(nil), w.secrets...) {
		if strings.ContainsAny(secret, "\r\n") {
			for _, line := range strings.FieldsFunc(secret, func(r rune) bool { return r == '\r' || r == '\n' }) {
				w.secrets = append(w.secrets, line)
			}
		}
	}
	slices.SortFunc(w.secrets, func(a, b string) int { return len(b) - len(a) })
	seen := map[string]bool{}
	filtered := w.secrets[:0]
	for _, secret := range w.secrets {
		if secret != "" && !seen[secret] {
			seen[secret] = true
			filtered = append(filtered, secret)
		}
	}
	w.secrets = filtered
	for _, secret := range w.secrets {
		if len(secret) > w.hold {
			w.hold = len(secret)
		}
	}
	return w
}

func (w *redactingWriter) redact(value string) string {
	covered := make([]bool, len(value))
	w.markSecrets(value, covered)
	redacted, _ := maskPrefix(value, covered, len(value), false)
	return redacted
}

// Keep matches on raw bytes, including bytes retained for a later Write. An
// overlapping match can extend coverage beyond a secret already partly emitted.
func (w *redactingWriter) markSecrets(value string, covered []bool) {
	for _, secret := range w.secrets {
		for start := 0; start <= len(value)-len(secret); {
			index := strings.Index(value[start:], secret)
			if index < 0 {
				break
			}
			index += start
			for i := index; i < index+len(secret); i++ {
				covered[i] = true
			}
			start = index + 1
		}
	}
}

// masked carries a redacted run across writes; replacement markers never
// become input for matching, even when a secret occurs inside "[redacted]".
func maskPrefix(raw string, covered []bool, limit int, masked bool) (string, bool) {
	var out strings.Builder
	for i := 0; i < limit; i++ {
		if covered[i] {
			if !masked {
				out.WriteString("[redacted]")
			}
			masked = true
		} else {
			out.WriteByte(raw[i])
			masked = false
		}
	}
	return out.String(), masked
}

func (w *redactingWriter) writeOutput(value string) error {
	if value == "" {
		return nil
	}
	n, err := io.WriteString(w.out, value)
	if err == nil && n != len(value) {
		err = io.ErrShortWrite
	}
	w.err = err
	return err
}

func (w *redactingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	w.pending += string(p)
	w.covered = append(w.covered, make([]bool, len(p))...)
	w.markSecrets(w.pending, w.covered)
	n := len(w.pending) - w.hold
	if n > 0 {
		value, masked := maskPrefix(w.pending, w.covered, n, w.masked)
		if err := w.writeOutput(value); err != nil {
			return 0, err
		}
		w.pending = strings.Clone(w.pending[n:])
		w.covered = append([]bool(nil), w.covered[n:]...)
		w.masked = masked
	}
	return len(p), nil
}

func (w *redactingWriter) flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	w.markSecrets(w.pending, w.covered)
	value, _ := maskPrefix(w.pending, w.covered, len(w.pending), w.masked)
	if err := w.writeOutput(value); err != nil {
		return err
	}
	w.pending = ""
	w.covered = nil
	w.masked = false
	return nil
}
