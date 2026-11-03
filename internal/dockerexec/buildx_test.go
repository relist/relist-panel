package dockerexec

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"relist-panel/internal/builders"
)

// cliRecorder stands in for the docker CLI: it records every argv, captures
// the buildkitd config a create passes, and fails the argvs fail matches.
type cliRecorder struct {
	mu     sync.Mutex
	calls  [][]string
	config string // contents of --buildkitd-config at call time
	fail   func(args []string) bool
}

func recordCLI(t *testing.T, fail func(args []string) bool) *cliRecorder {
	t.Helper()
	r := &cliRecorder{fail: fail}
	old := command
	command = func(ctx context.Context, args ...string) *exec.Cmd {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.calls = append(r.calls, append([]string(nil), args...))
		for i, a := range args {
			if a == "--buildkitd-config" && i+1 < len(args) {
				data, _ := os.ReadFile(args[i+1])
				r.config = string(data)
			}
		}
		if r.fail != nil && r.fail(args) {
			return exec.CommandContext(ctx, "sh", "-c", "echo 'ERROR: no builder found' >&2; exit 1")
		}
		return exec.CommandContext(ctx, "true")
	}
	t.Cleanup(func() { command = old })
	return r
}

// joined returns each recorded argv as one space-separated string.
func (r *cliRecorder) joined() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	for i, c := range r.calls {
		out[i] = strings.Join(c, " ")
	}
	return out
}

// containerDaemon serves GET /containers/<name>/json from state; a nil entry
// is a 404.
type containerDaemon struct {
	mu         sync.Mutex
	containers map[string]map[string]any
	volumes    []map[string]any
	deleted    []string
}

func (d *containerDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json")
		c, ok := d.containers[name]
		if !ok {
			http.Error(w, `{"message":"No such container"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(c)
	case path == "/system/df":
		_ = json.NewEncoder(w).Encode(map[string]any{"Volumes": d.volumes})
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/volumes/"):
		d.deleted = append(d.deleted, strings.TrimPrefix(path, "/volumes/"))
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func container(running bool, image string, quota, memory int64) map[string]any {
	return map[string]any{
		"State":      map[string]any{"Running": running},
		"Config":     map[string]any{"Image": image},
		"HostConfig": map[string]any{"CpuQuota": quota, "CpuPeriod": 100000, "Memory": memory, "MemorySwap": memory},
	}
}

var spec3 = builders.HostSpec{Name: "relist-builder-3", Image: builders.DefaultBuildKitImage, CPUQuota: 150000, CPUPeriod: 100000, MemoryBytes: 2 << 30, MaxParallelism: 4}

const container3 = "buildx_buildkit_relist-builder-30"

func newBuildx(t *testing.T, d *containerDaemon) *Buildx {
	t.Helper()
	if d.containers == nil {
		d.containers = map[string]map[string]any{}
	}
	socket := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen on %s: %v", socket, err)
	}
	server := &http.Server{Handler: d}
	go server.Serve(listener)
	t.Cleanup(func() { _ = server.Close() })
	return NewBuildx(socket)
}

func createArgs(config bool) string {
	s := "buildx create --name relist-builder-3 --driver docker-container --driver-opt image=" + builders.DefaultBuildKitImage +
		",memory=2147483648,memory-swap=2147483648,cpu-quota=150000,cpu-period=100000"
	if config {
		s += " --buildkitd-config"
	}
	return s
}

func TestEnsureCreateArgs(t *testing.T) {
	cli := recordCLI(t, nil)
	b := newBuildx(t, &containerDaemon{})
	if err := b.Ensure(context.Background(), spec3); err != nil {
		t.Fatal(err)
	}
	calls := cli.joined()
	if len(calls) != 3 {
		t.Fatalf("calls = %q", calls)
	}
	if calls[0] != "buildx rm --keep-state relist-builder-3" {
		t.Fatalf("first call = %q, want the stale instance dropped", calls[0])
	}
	if !strings.HasPrefix(calls[1], createArgs(true)+" ") {
		t.Fatalf("create = %q\nwant prefix %q", calls[1], createArgs(true))
	}
	if calls[2] != "buildx inspect --bootstrap relist-builder-3" {
		t.Fatalf("bootstrap = %q", calls[2])
	}
	if !strings.Contains(cli.config, "max-parallelism = 4") {
		t.Fatalf("config = %q", cli.config)
	}
	tmp := cli.calls[1][len(cli.calls[1])-1]
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("config file %s left behind (stat err %v)", tmp, err)
	}
}

func TestEnsureNoConfigWithoutParallelism(t *testing.T) {
	cli := recordCLI(t, nil)
	b := newBuildx(t, &containerDaemon{})
	spec := spec3
	spec.MaxParallelism = 0
	if err := b.Ensure(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if got := cli.joined()[1]; got != createArgs(false) {
		t.Fatalf("create = %q, want %q", got, createArgs(false))
	}
}

func TestEnsureAttachesToExistingContainer(t *testing.T) {
	// A fresh ~/.docker (a rebuilt panel) does not know the instance: Ensure
	// creates it, which attaches to the existing container.
	cli := recordCLI(t, func(args []string) bool {
		return reflect.DeepEqual(args, []string{"buildx", "inspect", "relist-builder-3"})
	})
	b := newBuildx(t, &containerDaemon{containers: map[string]map[string]any{container3: container(true, builders.DefaultBuildKitImage, 150000, 2<<30)}})
	if err := b.Ensure(context.Background(), spec3); err != nil {
		t.Fatal(err)
	}
	calls := cli.joined()
	if len(calls) != 3 || calls[0] != "buildx inspect relist-builder-3" || !strings.HasPrefix(calls[1], createArgs(true)) || calls[2] != "buildx inspect --bootstrap relist-builder-3" {
		t.Fatalf("calls = %q", calls)
	}
}

func TestEnsureUpdatesDriftedLimits(t *testing.T) {
	update := "update --cpu-quota 150000 --cpu-period 100000 --memory 2147483648 --memory-swap 2147483648 " + container3
	for name, tc := range map[string]struct {
		quota, memory int64
		want          bool
	}{
		"matching":     {150000, 2 << 30, false},
		"memory drift": {150000, 1 << 30, true},
		"cpu drift":    {50000, 2 << 30, true},
	} {
		t.Run(name, func(t *testing.T) {
			cli := recordCLI(t, nil)
			b := newBuildx(t, &containerDaemon{containers: map[string]map[string]any{container3: container(true, builders.DefaultBuildKitImage, tc.quota, tc.memory)}})
			if err := b.Ensure(context.Background(), spec3); err != nil {
				t.Fatal(err)
			}
			calls := cli.joined()
			got := calls[len(calls)-1] == update
			if got != tc.want {
				t.Fatalf("calls = %q, want update %v", calls, tc.want)
			}
		})
	}
}

func TestRecreateKeepsState(t *testing.T) {
	cli := recordCLI(t, nil)
	b := newBuildx(t, &containerDaemon{containers: map[string]map[string]any{container3: container(true, "old@sha256:x", 150000, 2<<30)}})
	if err := b.Recreate(context.Background(), spec3); err != nil {
		t.Fatal(err)
	}
	calls := cli.joined()
	rm := -1
	create := -1
	for i, c := range calls {
		if c == "buildx rm --keep-state relist-builder-3" && rm < 0 {
			rm = i
		}
		if strings.HasPrefix(c, createArgs(true)) {
			create = i
		}
	}
	if rm < 0 || create < rm {
		t.Fatalf("calls = %q, want rm --keep-state before the create", calls)
	}
	for _, c := range calls {
		if c == "buildx rm relist-builder-3" {
			t.Fatalf("Recreate dropped the state: %q", calls)
		}
	}
}

func TestRemoveDropsState(t *testing.T) {
	cli := recordCLI(t, nil)
	d := &containerDaemon{containers: map[string]map[string]any{container3: container(true, builders.DefaultBuildKitImage, 150000, 2<<30)}}
	b := newBuildx(t, d)
	if err := b.Remove(context.Background(), "relist-builder-3"); err != nil {
		t.Fatal(err)
	}
	calls := cli.joined()
	if calls[len(calls)-1] != "buildx rm relist-builder-3" {
		t.Fatalf("calls = %q, want buildx rm last", calls)
	}
	if !reflect.DeepEqual(d.deleted, []string{container3 + "_state"}) {
		t.Fatalf("deleted volumes = %v", d.deleted)
	}
}

func TestRemoveMissingIsNotAnError(t *testing.T) {
	recordCLI(t, func(args []string) bool { return true })
	b := newBuildx(t, &containerDaemon{})
	if err := b.Remove(context.Background(), "relist-builder-3"); err != nil {
		t.Fatalf("Remove of a missing builder = %v", err)
	}
}

func TestDiskUsageReadsStateVolume(t *testing.T) {
	recordCLI(t, nil)
	b := newBuildx(t, &containerDaemon{volumes: []map[string]any{
		{"Name": "other", "UsageData": map[string]any{"Size": 5}},
		{"Name": container3 + "_state", "UsageData": map[string]any{"Size": 4172000000}},
	}})
	got, err := b.DiskUsage(context.Background(), "relist-builder-3")
	if err != nil || got != 4172000000 {
		t.Fatalf("DiskUsage = %d, %v", got, err)
	}
	if got, err := b.DiskUsage(context.Background(), "relist-builder-4"); err != nil || got != 0 {
		t.Fatalf("DiskUsage missing = %d, %v", got, err)
	}
}

func TestInspect(t *testing.T) {
	recordCLI(t, nil)
	b := newBuildx(t, &containerDaemon{containers: map[string]map[string]any{
		container3:                          container(true, "img@sha256:a", 0, 0),
		"buildx_buildkit_relist-builder-40": container(false, "img@sha256:b", 0, 0),
	}})
	ctx := context.Background()
	for name, want := range map[string]builders.Inspection{
		"relist-builder-3": {State: builders.StateRunning, Image: "img@sha256:a"},
		"relist-builder-4": {State: builders.StateStopped, Image: "img@sha256:b"},
		"relist-builder-5": {State: builders.StateMissing},
	} {
		got, err := b.Inspect(ctx, name)
		if err != nil || got != want {
			t.Errorf("Inspect(%s) = %+v, %v; want %+v", name, got, err, want)
		}
	}
}

func TestPullArgs(t *testing.T) {
	cli := recordCLI(t, nil)
	b := newBuildx(t, &containerDaemon{})
	if err := b.Pull(context.Background(), "moby/buildkit@sha256:x"); err != nil {
		t.Fatal(err)
	}
	if got := cli.joined(); !reflect.DeepEqual(got, []string{"pull moby/buildkit@sha256:x"}) {
		t.Fatalf("calls = %q", got)
	}
}

func TestPruneArgs(t *testing.T) {
	cli := recordCLI(t, nil)
	b := newBuildx(t, &containerDaemon{})
	if err := b.Prune(context.Background(), "relist-builder-3"); err != nil {
		t.Fatal(err)
	}
	if got := cli.joined(); !reflect.DeepEqual(got, []string{"buildx prune -af --builder relist-builder-3"}) {
		t.Fatalf("calls = %q", got)
	}
}
