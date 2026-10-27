package dockerexec

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"relist-panel/internal/vulnscan"
)

const testImageID = "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// serveEngine serves handler on a unix socket, the transport the panel uses.
func serveEngine(t *testing.T, handler http.Handler) string {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return socket
}

// fakeEngine answers inspect and export for one image of size bytes.
func fakeEngine(t *testing.T, size int64, gone bool) string {
	return serveEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case gone:
			http.Error(w, `{"message":"No such image"}`, http.StatusNotFound)
		case strings.HasSuffix(r.URL.Path, "/json"):
			io.WriteString(w, `{"Id":"`+testImageID+`","Size":`+strconv.FormatInt(size, 10)+`}`)
		case strings.HasSuffix(r.URL.Path, "/get"):
			io.WriteString(w, "tar bytes")
		default:
			http.NotFound(w, r)
		}
	}))
}

type exitCode int

func (e exitCode) Error() string { return "exit status " + strconv.Itoa(int(e)) }
func (e exitCode) ExitCode() int { return int(e) }

// fakeDocker records every docker invocation and answers it with respond.
type fakeDocker struct {
	mu      sync.Mutex
	calls   [][]string
	respond func(argv []string, stdout io.Writer) error
}

func (f *fakeDocker) run(_ context.Context, argv []string, _ io.Reader, stdout, _ io.Writer) error {
	f.mu.Lock()
	f.calls = append(f.calls, argv)
	f.mu.Unlock()
	if f.respond == nil {
		return nil
	}
	return f.respond(argv, stdout)
}

func (f *fakeDocker) called(sub string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c, " "), sub) {
			n++
		}
	}
	return n
}

func newTestScanner(t *testing.T, socket string, d *fakeDocker, free uint64) (*TrivyScanner, string) {
	dataDir := t.TempDir()
	s := NewTrivyScanner(socket, dataDir)
	s.run = d.run
	s.free = func(string) (uint64, error) { return free, nil }
	return s, filepath.Join(dataDir, ".scan-work")
}

func workFiles(t *testing.T, dir string) []string {
	entries, _ := os.ReadDir(dir)
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestScanParsesAndRemovesTar(t *testing.T) {
	fixture, _ := os.ReadFile(filepath.Join("testdata", "trivy", "os-alpine.json"))
	var tarSeen bool
	d := &fakeDocker{}
	s, work := newTestScanner(t, fakeEngine(t, 1000, false), d, 1<<40)
	d.respond = func(argv []string, stdout io.Writer) error {
		for _, a := range argv {
			if strings.HasSuffix(a, ":/work/image.tar:ro") {
				path := strings.TrimSuffix(a, ":/work/image.tar:ro")
				info, err := os.Stat(path)
				tarSeen = err == nil && info.Mode().Perm() == 0o644
			}
		}
		_, err := stdout.Write(fixture)
		return err
	}
	r, err := s.Scan(context.Background(), testScannerRef, testImageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 2 || !tarSeen {
		t.Fatalf("findings %d, tar seen %v", len(r.Findings), tarSeen)
	}
	if left := workFiles(t, work); len(left) != 0 {
		t.Fatalf("left in .scan-work: %v", left)
	}
	if d.called("docker.sock") != 0 {
		t.Fatal("socket passed to the scanner")
	}
}

func TestScanRemovesTarOnFailure(t *testing.T) {
	d := &fakeDocker{respond: func([]string, io.Writer) error { return exitCode(1) }}
	s, work := newTestScanner(t, fakeEngine(t, 1000, false), d, 1<<40)
	if _, err := s.Scan(context.Background(), testScannerRef, testImageID); err == nil {
		t.Fatal("want error")
	}
	if left := workFiles(t, work); len(left) != 0 {
		t.Fatalf("left in .scan-work: %v", left)
	}
}

func TestScanImageGone(t *testing.T) {
	d := &fakeDocker{}
	s, _ := newTestScanner(t, fakeEngine(t, 0, true), d, 1<<40)
	_, err := s.Scan(context.Background(), testScannerRef, testImageID)
	if !errors.Is(err, vulnscan.ErrImageGone) {
		t.Fatalf("err = %v", err)
	}
	if len(d.calls) != 0 {
		t.Fatalf("docker ran: %v", d.calls)
	}
}

func TestScanDiskShort(t *testing.T) {
	d := &fakeDocker{}
	s, work := newTestScanner(t, fakeEngine(t, 1<<30, false), d, 1<<30) // needs size + 512 MiB
	_, err := s.Scan(context.Background(), testScannerRef, testImageID)
	if err == nil || err.Error() != "not enough disk space for export" {
		t.Fatalf("err = %v", err)
	}
	if left := workFiles(t, work); len(left) != 0 || len(d.calls) != 0 {
		t.Fatalf("left %v, calls %v", left, d.calls)
	}
}

func TestScanOOMMessage(t *testing.T) {
	d := &fakeDocker{respond: func([]string, io.Writer) error { return exitCode(137) }}
	s, _ := newTestScanner(t, fakeEngine(t, 1000, false), d, 1<<40)
	_, err := s.Scan(context.Background(), testScannerRef, testImageID)
	if err == nil || err.Error() != "scanner ran out of memory (1 GB limit)" {
		t.Fatalf("err = %v", err)
	}
}

func TestScanRejectsBadImageID(t *testing.T) {
	d := &fakeDocker{}
	s, _ := newTestScanner(t, fakeEngine(t, 1000, false), d, 1<<40)
	if _, err := s.Scan(context.Background(), testScannerRef, "sha256:../../etc"); err == nil {
		t.Fatal("want error")
	}
}

func TestEnsureImagePullsOnlyWhenMissing(t *testing.T) {
	present := &fakeDocker{}
	s, _ := newTestScanner(t, fakeEngine(t, 0, false), present, 1<<40)
	if err := s.EnsureImage(context.Background(), testScannerRef); err != nil {
		t.Fatal(err)
	}
	if present.called("pull") != 0 || present.called("--version") != 1 || present.called("chown") != 1 {
		t.Fatalf("calls = %v", present.calls)
	}

	missing := &fakeDocker{respond: func(argv []string, _ io.Writer) error {
		if len(argv) > 2 && argv[1] == "image" && argv[2] == "inspect" {
			return exitCode(1)
		}
		return nil
	}}
	s.run = missing.run
	if err := s.EnsureImage(context.Background(), testScannerRef); err != nil {
		t.Fatal(err)
	}
	if missing.called("docker pull "+testScannerRef) != 1 {
		t.Fatalf("calls = %v", missing.calls)
	}
}

func TestEnsureImagePullFails(t *testing.T) {
	d := &fakeDocker{respond: func(argv []string, _ io.Writer) error {
		if argv[1] == "image" || argv[1] == "pull" {
			return exitCode(1)
		}
		return nil
	}}
	s, _ := newTestScanner(t, fakeEngine(t, 0, false), d, 1<<40)
	if err := s.EnsureImage(context.Background(), testScannerRef); err == nil {
		t.Fatal("want error")
	}
	if d.called("--version") != 0 {
		t.Fatal("ran an image that failed to pull")
	}
}

func TestDBInfo(t *testing.T) {
	answer := `{"Version":"0.76.1"}`
	d := &fakeDocker{respond: func(argv []string, stdout io.Writer) error {
		io.WriteString(stdout, answer)
		return nil
	}}
	s, _ := newTestScanner(t, fakeEngine(t, 0, false), d, 1<<40)
	if _, err := s.DBInfo(context.Background(), testScannerRef); !errors.Is(err, vulnscan.ErrNoDB) {
		t.Fatalf("err = %v", err)
	}
	answer = `{"Version":"0.76.1","VulnerabilityDB":{"Version":2,"UpdatedAt":"2026-10-27T13:12:49.474279883Z"}}`
	info, err := s.DBInfo(context.Background(), testScannerRef)
	if err != nil || info.Version != "2026-10-27T13:12:49.474279883Z" {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if d.called("--network none") != 2 {
		t.Fatalf("DBInfo must run offline: %v", d.calls)
	}
}

func TestCleanupRemovesLabelledAndWorkFiles(t *testing.T) {
	d := &fakeDocker{respond: func(argv []string, stdout io.Writer) error {
		if argv[1] == "ps" {
			io.WriteString(stdout, "abc\ndef\n")
		}
		return nil
	}}
	s, work := newTestScanner(t, fakeEngine(t, 0, false), d, 1<<40)
	os.MkdirAll(work, 0o755)
	os.WriteFile(filepath.Join(work, "x.tar"), []byte("x"), 0o644)
	if err := s.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d.called("ps -aq --filter label=relist.vulnscan=1") != 1 || d.called("rm -f abc def") != 1 {
		t.Fatalf("calls = %v", d.calls)
	}
	if left := workFiles(t, work); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}

func TestCacheSize(t *testing.T) {
	socket := serveEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"Volumes":[{"Name":"other","UsageData":{"Size":5}},{"Name":"relist-trivy-cache","UsageData":{"Size":1500}}]}`)
	}))
	s, _ := newTestScanner(t, socket, &fakeDocker{}, 1<<40)
	n, err := s.CacheSize(context.Background())
	if err != nil || n != 1500 {
		t.Fatalf("CacheSize = %d, %v", n, err)
	}
}

// TestTrivyScannerIntegration scans a real image end to end. It pulls the
// Scanner image and creates the relist-trivy-cache volume on the host.
func TestTrivyScannerIntegration(t *testing.T) {
	if os.Getenv("RELIST_DOCKER_TESTS") != "1" || exec.Command("docker", "info").Run() != nil {
		t.Skip("set RELIST_DOCKER_TESTS=1 with a reachable Docker daemon")
	}
	ctx := context.Background()
	if err := exec.Command("docker", "pull", "-q", "traefik/whoami").Run(); err != nil {
		t.Fatal(err)
	}
	id, err := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", "traefik/whoami").Output()
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	s := NewTrivyScanner(DefaultSocketPath, dataDir)
	if err := s.EnsureImage(ctx, testScannerRef); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateDB(ctx, testScannerRef); err != nil {
		t.Fatal(err)
	}
	r, err := s.Scan(ctx, testScannerRef, strings.TrimSpace(string(id)))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) == 0 {
		t.Fatal("no findings for traefik/whoami")
	}
	if left := workFiles(t, filepath.Join(dataDir, ".scan-work")); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}
