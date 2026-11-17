package agefile

import (
	"bytes"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"
)

const pass = "test-passphrase-0123456789"

func encrypt(t *testing.T, passphrase, plain string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Encrypt(&buf, passphrase, strings.NewReader(plain)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRoundTripAndHeader(t *testing.T) {
	enc := encrypt(t, pass, "hello")
	if !bytes.HasPrefix(enc, []byte("age-encryption.org/v1\n")) {
		t.Fatalf("header = %q", enc[:min(len(enc), 30)])
	}
	if !IsAge(enc[:22]) {
		t.Fatal("IsAge(encrypted prefix) = false")
	}
	if IsAge([]byte{0x1f, 0x8b, 0x08, 0x00}) {
		t.Fatal("IsAge(gzip magic) = true")
	}
	r, err := Decrypt(pass, bytes.NewReader(enc))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("decrypted = %q", got)
	}
}

func TestWrongPassphrase(t *testing.T) {
	enc := encrypt(t, pass, "hello")
	_, err := Decrypt("another-passphrase-000000", bytes.NewReader(enc))
	if !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("err = %v, want ErrWrongPassphrase", err)
	}
}

// The spec pins the work factor at 2^15 = 32 MiB: one derivation must cost
// well under the 2^18 default's 256 MiB, which would OOM the panel container.
func TestDerivationMemory(t *testing.T) {
	enc := encrypt(t, pass, "hello")
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if _, err := Decrypt(pass, bytes.NewReader(enc)); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	delta := after.TotalAlloc - before.TotalAlloc
	if delta < 16<<20 {
		t.Fatalf("TotalAlloc delta = %d, the measurement sees no derivation", delta)
	}
	if delta >= 128<<20 {
		t.Fatalf("TotalAlloc delta = %d (>= 128 MiB): the work factor is not 2^15", delta)
	}
}

// Review Focus 5: a header above 2^15 is refused before any derivation, so
// an uploaded file cannot make the panel allocate gigabytes.
func TestTooExpensiveRefusedWithoutDeriving(t *testing.T) {
	enc := encrypt(t, pass, "hello")
	head := enc
	if i := bytes.Index(head, []byte(" 15")); i < 0 {
		t.Fatalf("no \" 15\" in the scrypt stanza: %q", head[:min(len(head), 80)])
	}
	tampered := bytes.Replace(enc, []byte(" 15"), []byte(" 22"), 1)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := Decrypt(pass, bytes.NewReader(tampered))
	runtime.ReadMemStats(&after)
	if !errors.Is(err, ErrTooExpensive) {
		t.Fatalf("err = %v, want ErrTooExpensive", err)
	}
	if delta := after.TotalAlloc - before.TotalAlloc; delta >= 8<<20 {
		t.Fatalf("TotalAlloc delta = %d: the refusal derived anyway", delta)
	}
}

func TestOneDerivationAtATime(t *testing.T) {
	mu.Lock()
	done := make(chan struct{})
	go func() {
		var buf bytes.Buffer
		_ = Encrypt(&buf, pass, strings.NewReader("hello"))
		close(done)
	}()
	select {
	case <-done:
		mu.Unlock()
		t.Fatal("Encrypt derived while the package mutex was held")
	case <-time.After(100 * time.Millisecond):
	}
	mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Encrypt never finished after unlock")
	}
}
