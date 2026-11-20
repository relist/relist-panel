package dockerexec_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"testing"

	"relist-panel/internal/databases"
	"relist-panel/internal/dockerexec"
)

func frame(stream byte, payload []byte) []byte {
	h := make([]byte, 8)
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], uint32(len(payload)))
	return append(h, payload...)
}

func TestDemuxStream(t *testing.T) {
	var src bytes.Buffer
	src.Write(frame(1, []byte("ab")))
	src.Write(frame(2, []byte("x")))
	src.Write(frame(1, []byte("c")))
	var out, errOut bytes.Buffer
	if err := dockerexec.DemuxStream(&src, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if out.String() != "abc" || errOut.String() != "x" {
		t.Fatalf("stdout %q stderr %q", out.String(), errOut.String())
	}
	if err := dockerexec.DemuxStream(bytes.NewReader([]byte{1, 0, 0}), io.Discard, io.Discard); err == nil {
		t.Fatal("a truncated header was accepted")
	}
	if err := dockerexec.DemuxStream(bytes.NewReader(frame(1, []byte("abcd"))[:10]), io.Discard, io.Discard); err == nil {
		t.Fatal("a truncated payload was accepted")
	}
}

type execDaemon struct {
	mu     sync.Mutex
	create []byte
	path   string
	code   int
	reply  func(stdin []byte) []byte
}

func (d *execDaemon) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /containers/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.create, d.path = body, r.URL.Path
		d.mu.Unlock()
		writeTerminalJSON(t, w, http.StatusCreated, map[string]string{"Id": "e1"})
	})
	mux.HandleFunc("POST /exec/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: tcp\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		var stdin []byte
		d.mu.Lock()
		hasStdin := strings.Contains(string(d.create), `"AttachStdin":true`)
		d.mu.Unlock()
		if hasStdin {
			stdin, _ = io.ReadAll(rw) // until the client's half-close
		}
		_, _ = rw.Write(d.reply(stdin))
		_ = rw.Flush()
	})
	mux.HandleFunc("GET /exec/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		writeTerminalJSON(t, w, http.StatusOK, map[string]any{"Running": false, "ExitCode": d.code})
	})
	return mux
}

func TestDatabaseExecStreamsStdinAndOutput(t *testing.T) {
	d := &execDaemon{code: 3}
	d.reply = func(stdin []byte) []byte {
		return append(frame(1, stdin), frame(2, []byte("warn"))...)
	}
	host := dockerexec.NewDatabaseHost(newTerminalDaemon(t, d.handler(t)))
	in := make([]byte, 1<<20)
	rand.New(rand.NewSource(1)).Read(in)
	var out, errOut bytes.Buffer
	code, err := host.Exec(context.Background(), "relist-db-1", databases.ExecRequest{
		Cmd: []string{"cat"}, Env: []string{"PGPASSWORD=s3cret"}, Stdin: bytes.NewReader(in), Stdout: &out, Stderr: &errOut,
	})
	if err != nil || code != 3 {
		t.Fatalf("Exec = %d, %v", code, err)
	}
	if !bytes.Equal(out.Bytes(), in) || errOut.String() != "warn" {
		t.Fatalf("stdout %d bytes (want %d), stderr %q", out.Len(), len(in), errOut.String())
	}
	var body struct {
		AttachStdin, Tty bool
		Env, Cmd         []string
	}
	if err := json.Unmarshal(d.create, &body); err != nil {
		t.Fatal(err)
	}
	if !body.AttachStdin || body.Tty || len(body.Env) != 1 || body.Env[0] != "PGPASSWORD=s3cret" || body.Cmd[0] != "cat" {
		t.Fatalf("create body = %s", d.create)
	}
	if d.path != "/containers/relist-db-1/exec" {
		t.Fatalf("path = %s", d.path)
	}
}

func TestDatabaseExecWithoutStdin(t *testing.T) {
	d := &execDaemon{}
	d.reply = func([]byte) []byte { return frame(1, []byte("out")) }
	host := dockerexec.NewDatabaseHost(newTerminalDaemon(t, d.handler(t)))
	var out bytes.Buffer
	code, err := host.Exec(context.Background(), "c", databases.ExecRequest{Cmd: []string{"echo"}, Stdout: &out})
	if err != nil || code != 0 || out.String() != "out" {
		t.Fatalf("Exec = %d, %v, %q", code, err, out.String())
	}
	if strings.Contains(string(d.create), `"AttachStdin":true`) {
		t.Fatalf("create body = %s", d.create)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, io.ErrShortWrite }

func TestDatabaseExecReturnsStdoutWriteError(t *testing.T) {
	d := &execDaemon{}
	d.reply = func([]byte) []byte { return frame(1, []byte("out")) }
	host := dockerexec.NewDatabaseHost(newTerminalDaemon(t, d.handler(t)))
	if _, err := host.Exec(context.Background(), "c", databases.ExecRequest{Cmd: []string{"x"}, Stdout: failWriter{}}); err == nil {
		t.Fatal("a failed stdout write was not returned")
	}
}

// panicReader panics on its first read: a fault in the stdin chain (gzip,
// age, the upload checks) runs on Exec's copy goroutine.
type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("boom") }

func TestDatabaseExecContainsAPanicReadingStdin(t *testing.T) {
	d := &execDaemon{}
	d.reply = func([]byte) []byte { return frame(1, []byte("out")) }
	host := dockerexec.NewDatabaseHost(newTerminalDaemon(t, d.handler(t)))
	var out bytes.Buffer
	_, err := host.Exec(context.Background(), "c", databases.ExecRequest{Cmd: []string{"psql"}, Stdin: panicReader{}, Stdout: &out})
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("Exec = %v, want the stdin panic as an error", err)
	}
}
