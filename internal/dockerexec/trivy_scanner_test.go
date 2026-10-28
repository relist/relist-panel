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
	"time"

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
	// hang makes matching invocations wait for their context, as a stuck CLI
	// would until exec.CommandContext kills it.
	hang func(argv []string) bool
}

func (f *fakeDocker) run(ctx context.Context, argv []string, _ io.Reader, stdout, _ io.Writer) error {
	f.mu.Lock()
	f.calls = append(f.calls, argv)
	f.mu.Unlock()
	if f.hang != nil && f.hang(argv) {
		<-ctx.Done()
		return exitCode(137)
	}
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
	if present.called("pull") != 0 || present.called("--version") != 1 || present.called("chown") != 0 {
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

func TestEnsureImageChownsOnlyANewVolume(t *testing.T) {
	d := &fakeDocker{respond: func(argv []string, _ io.Writer) error {
		if len(argv) > 2 && argv[1] == "volume" && argv[2] == "inspect" {
			return exitCode(1)
		}
		return nil
	}}
	s, _ := newTestScanner(t, fakeEngine(t, 0, false), d, 1<<40)
	if err := s.EnsureImage(context.Background(), testScannerRef); err != nil {
		t.Fatal(err)
	}
	if d.called("volume create relist-trivy-cache") != 1 || d.called("--entrypoint chown") != 1 {
		t.Fatalf("calls = %v", d.calls)
	}
	// A run that mounts the volume before it is created would create it owned
	// by root, and the inspect would then skip the chown.
	for _, c := range d.calls {
		line := strings.Join(c, " ")
		if strings.Contains(line, "--entrypoint chown") {
			break
		}
		if strings.Contains(line, "relist-trivy-cache:/cache") {
			t.Fatalf("mounted the cache before preparing it: %v", d.calls)
		}
	}
}

func TestChecksRunWithoutFixedNames(t *testing.T) {
	d := &fakeDocker{respond: func(argv []string, stdout io.Writer) error {
		io.WriteString(stdout, `{"Version":"0.76.1","VulnerabilityDB":{"UpdatedAt":"x"}}`)
		return nil
	}}
	s, _ := newTestScanner(t, fakeEngine(t, 0, false), d, 1<<40)
	s.EnsureImage(context.Background(), testScannerRef)
	s.DBInfo(context.Background(), testScannerRef)
	for _, c := range d.calls {
		joined := strings.Join(c, " ")
		if (strings.Contains(joined, "--version") || strings.Contains(joined, "version -f json")) && strings.Contains(joined, "--name") {
			t.Fatalf("a check run takes a fixed name another run can collide with: %v", c)
		}
	}
}

func TestScanCancelRemovesTheContainer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d := &fakeDocker{}
	d.respond = func(argv []string, _ io.Writer) error {
		if argv[1] == "run" && strings.Contains(strings.Join(argv, " "), "--input") {
			cancel() // the panel shuts down while the CLI waits on the container
			return exitCode(137)
		}
		return nil
	}
	s, _ := newTestScanner(t, fakeEngine(t, 1000, false), d, 1<<40)
	_, err := s.Scan(ctx, testScannerRef, testImageID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d.called("rm -f relist-vulnscan-"+strings.TrimPrefix(testImageID, "sha256:")[:12]) != 1 {
		t.Fatalf("container left running: %v", d.calls)
	}
}

func TestScanTimeoutSaysSoAndRemovesTheContainer(t *testing.T) {
	d := &fakeDocker{}
	s, _ := newTestScanner(t, fakeEngine(t, 1000, false), d, 1<<40)
	s.scanTimeout = 20 * time.Millisecond
	d.hang = func(argv []string) bool {
		return argv[1] == "run" && strings.Contains(strings.Join(argv, " "), "--input")
	}
	_, err := s.Scan(context.Background(), testScannerRef, testImageID)
	if err == nil || err.Error() != "scan timed out" {
		t.Fatalf("err = %v", err)
	}
	if d.called("rm -f relist-vulnscan-") != 1 {
		t.Fatalf("container left running: %v", d.calls)
	}
}

func TestUpdateDBTimeoutRemovesTheContainer(t *testing.T) {
	d := &fakeDocker{}
	s, _ := newTestScanner(t, fakeEngine(t, 0, false), d, 1<<40)
	s.dbUpdateTimeout = 20 * time.Millisecond
	d.hang = func(argv []string) bool { return strings.Contains(strings.Join(argv, " "), "--download-db-only") }
	if _, err := s.UpdateDB(context.Background(), testScannerRef); err == nil {
		t.Fatal("want error")
	}
	if d.called("rm -f relist-vulnscan-db") != 1 {
		t.Fatalf("container left running: %v", d.calls)
	}
}

func TestEnsureImageIsBounded(t *testing.T) {
	d := &fakeDocker{}
	s, _ := newTestScanner(t, fakeEngine(t, 0, false), d, 1<<40)
	s.ensureTimeout = 20 * time.Millisecond
	d.respond = func(argv []string, _ io.Writer) error {
		if argv[1] == "image" {
			return exitCode(1) // missing, so it pulls
		}
		return nil
	}
	d.hang = func(argv []string) bool { return argv[1] == "pull" }
	done := make(chan error, 1)
	go func() { done <- s.EnsureImage(context.Background(), testScannerRef) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("EnsureImage not bounded")
	}
}

func TestDockerErrorNamesTrivysFailure(t *testing.T) {
	for _, tc := range []struct{ stderr, want string }{
		{"2026-10-28T17:34:39Z\tINFO\t[vulndb] Need to update DB\n" +
			"2026-10-28T17:34:39Z\tINFO\t[vulndb] Downloading vulnerability DB...\n" +
			"2026-10-28T17:34:44Z\tFATAL\tFatal error\tinit error: DB error: failed to download vulnerability DB\n",
			"Fatal error: init error: DB error: failed to download vulnerability DB"},
		{"docker: Error response from daemon: conflict.\nRun 'docker run --help' for more information\n",
			"docker: Error response from daemon: conflict."},
	} {
		if err := dockerError(errors.New("exit status 1"), tc.stderr); err.Error() != tc.want {
			t.Errorf("err = %q, want %q", err, tc.want)
		}
	}
}
