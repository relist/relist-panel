package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/config"
)

// fakeSourceServer serves canned items the way the old host's routes do.
type fakeSourceServer struct {
	mu        sync.Mutex
	srv       *httptest.Server
	key       string
	manifest  Manifest
	bodies    map[string]string
	cutOnce   map[string]bool // first stream of the id stops halfway
	badDigest map[string]int  // how many times the id's digest is wrong
	starts    int
	aborted   bool
	finished  string
	served    []string
}

func newFakeSourceServer(t *testing.T, items ...Item) *fakeSourceServer {
	t.Helper()
	f := &fakeSourceServer{key: "sess", bodies: map[string]string{}, cutOnce: map[string]bool{}, badDigest: map[string]int{}}
	f.manifest = Manifest{Protocol: ProtocolVersion, SchemaVersion: 18, DataDir: "/var/lib/relist-panel", RunningComposeIDs: []int64{1}, Items: items, Warnings: []string{}}
	for _, it := range items {
		f.bodies[it.ID] = "body of " + it.ID
	}
	mux := http.NewServeMux()
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+f.key {
				writeJSONError(w, 401, "invalid migration session")
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("POST /api/migration/claim", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Token, ClaimedBy string }
		json.NewDecoder(r.Body).Decode(&in)
		if in.Token != "tok" {
			writeJSONError(w, 401, "invalid or expired migration token")
			return
		}
		writeJSON(w, 200, map[string]string{"sessionKey": f.key})
	})
	mux.HandleFunc("GET /api/migration/preflight", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, SourceInfo{Protocol: ProtocolVersion, SchemaVersion: 18, DataDir: "/var/lib/relist-panel",
			HostPaths: []HostPathCandidate{{Path: "/srv/up"}, {Path: "/etc/localtime", System: true}}, Volumes: []Item{}, Images: []Item{}, Warnings: []string{}})
	}))
	mux.HandleFunc("POST /api/migration/start", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.starts++
		n := f.starts
		f.mu.Unlock()
		if n == 1 {
			writeJSON(w, 202, map[string]string{"step": "stopping"})
			return
		}
		writeJSON(w, 200, f.manifest)
	}))
	mux.HandleFunc("GET /api/migration/items/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		f.mu.Lock()
		f.served = append(f.served, id)
		body, ok := f.bodies[id]
		cut := f.cutOnce[id]
		f.cutOnce[id] = false
		f.mu.Unlock()
		if !ok {
			writeJSONError(w, 404, "no such migration item")
			return
		}
		if cut {
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			io.WriteString(w, body[:len(body)/2])
			// Flush first: a connection that dies before any response is
			// retried by net/http itself, and the cut would go unseen.
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}
		io.WriteString(w, body)
	}))
	mux.HandleFunc("GET /api/migration/items/{id}/digest", auth(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		f.mu.Lock()
		sum := sha256.Sum256([]byte(f.bodies[id]))
		d := hex.EncodeToString(sum[:])
		if f.badDigest[id] > 0 {
			f.badDigest[id]--
			d = strings.Repeat("0", 64)
		}
		f.mu.Unlock()
		writeJSON(w, 200, map[string]string{"sha256": d})
	}))
	mux.HandleFunc("POST /api/migration/abort", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.aborted = true
		f.mu.Unlock()
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /api/migration/finish", auth(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ TargetURL string }
		json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.finished = in.TargetURL
		f.mu.Unlock()
		w.WriteHeader(204)
	}))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// recordingRestorer keeps what it restored.
type recordingRestorer struct {
	mu        sync.Mutex
	restored  map[string]string
	cleaned   []string
	discarded []string
	failNext  map[string]int
}

func newRecordingRestorer() *recordingRestorer {
	return &recordingRestorer{restored: map[string]string{}, failNext: map[string]int{}}
}

func (r *recordingRestorer) Restore(_ context.Context, item Item, body io.Reader) error {
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failNext[item.ID] > 0 {
		r.failNext[item.ID]--
		return errors.New("extract failed")
	}
	r.restored[item.ID] = string(b)
	return nil
}

func (r *recordingRestorer) Cleanup(_ context.Context, item Item) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleaned = append(r.cleaned, item.ID)
	delete(r.restored, item.ID)
	return nil
}

func (r *recordingRestorer) Discard(_ context.Context, items []Item) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, it := range items {
		r.discarded = append(r.discarded, it.ID)
	}
	return nil
}

type recordingFinalizer struct {
	mu    sync.Mutex
	began []Manifest
}

func (f *recordingFinalizer) Begin(_ context.Context, m Manifest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.began = append(f.began, m)
	return nil
}

func (f *recordingFinalizer) Complete(context.Context, Manifest) (Summary, error) {
	return Summary{Deployments: []SummaryDeployment{{ComposeID: 1, Number: 1}}, Warnings: []string{}, DNS: []string{"shop.example.com"}}, nil
}

func (f *recordingFinalizer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.began)
}

type targetFixture struct {
	src      *fakeSourceServer
	restorer *recordingRestorer
	final    *recordingFinalizer
	dataDir  string
	deps     TargetDeps
	target   *Target
}

func newTargetFixture(t *testing.T, items ...Item) *targetFixture {
	t.Helper()
	if items == nil {
		items = []Item{{ID: "database", Kind: KindDatabase}, {ID: "data-dir", Kind: KindDataDir}, {ID: "volume-a", Kind: KindVolume, Volume: &VolumeInfo{Name: "a"}}, {ID: "volume-b", Kind: KindVolume, Volume: &VolumeInfo{Name: "b"}}}
	}
	f := &targetFixture{src: newFakeSourceServer(t, items...), restorer: newRecordingRestorer(), final: &recordingFinalizer{}, dataDir: "/var/lib/relist-panel"}
	stateDir := t.TempDir()
	f.deps = TargetDeps{
		Local: LocalHost{DB: openDB(t), Docker: NewFakeDocker(), DataDir: f.dataDir, Install: config.InstallSettings{},
			FreeBytes: func(string) (int64, error) { return 1 << 40, nil }},
		StateDir: stateDir, Insecure: true, Restore: f.restorer, Finalizer: f.final,
		Backoff: func(int) time.Duration { return 0 }, Context: context.Background(),
	}
	f.target = NewTarget(f.deps)
	return f
}

func (f *targetFixture) connectAndStart(t *testing.T) {
	t.Helper()
	if _, err := f.target.Connect(context.Background(), f.src.srv.URL, "tok", "https://new.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := f.target.Start(context.Background(), []string{"/srv/up"}); err != nil {
		t.Fatal(err)
	}
}

func waitPhase(t *testing.T, target *Target, phase Phase) Progress {
	t.Helper()
	var p Progress
	eventuallyTrue(t, "phase "+string(phase), func() bool {
		p = target.Progress()
		return p.Phase == phase
	})
	return p
}

func TestConnectRefusesHTTP(t *testing.T) {
	f := newTargetFixture(t)
	f.deps.Insecure = false
	target := NewTarget(f.deps)
	_, err := target.Connect(context.Background(), f.src.srv.URL, "tok", "https://new")
	if err == nil || err.Error() != "The source must use https://" {
		t.Fatalf("Connect(http) error = %v", err)
	}
}

func TestConnectSelectsNonSystemPaths(t *testing.T) {
	f := newTargetFixture(t)
	p, err := f.target.Connect(context.Background(), f.src.srv.URL, "tok", "https://new")
	if err != nil {
		t.Fatal(err)
	}
	if p.Phase != PhaseConnected || !slices.Equal(p.Selected, []string{"/srv/up"}) || p.Info == nil {
		t.Fatalf("progress = %+v", p)
	}
}

func TestStartRefusedWithBlockingCheck(t *testing.T) {
	f := newTargetFixture(t)
	f.deps.Local.DataDir = "/elsewhere"
	target := NewTarget(f.deps)
	if _, err := target.Connect(context.Background(), f.src.srv.URL, "tok", "https://new"); err != nil {
		t.Fatal(err)
	}
	err := target.Start(context.Background(), nil)
	var blocked *BlockedError
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Message, "RELIST_DATA_DIR differs") {
		t.Fatalf("Start() error = %v, want BlockedError", err)
	}
}

func TestPullRetriesThenSucceeds(t *testing.T) {
	f := newTargetFixture(t)
	f.src.cutOnce["volume-a"] = true
	f.connectAndStart(t)
	p := waitPhase(t, f.target, PhaseFinalizing)
	eventuallyTrue(t, "Begin", func() bool { return f.final.count() == 1 })
	if f.restorer.restored["volume-a"] != "body of volume-a" {
		t.Fatalf("volume-a restored as %q", f.restorer.restored["volume-a"])
	}
	for _, it := range p.Items {
		if it.ID == "volume-a" && it.Attempts != 1 {
			t.Fatalf("volume-a attempts = %d, want 1 failure recorded", it.Attempts)
		}
		if it.State != "done" {
			t.Fatalf("item %s = %s", it.ID, it.State)
		}
	}
	if !slices.Contains(f.restorer.cleaned, "volume-a") {
		t.Fatal("the cut attempt was not cleaned up")
	}
}

func TestDigestMismatchRetries(t *testing.T) {
	f := newTargetFixture(t)
	f.src.badDigest["data-dir"] = 1
	f.connectAndStart(t)
	waitPhase(t, f.target, PhaseFinalizing)
	count := 0
	for _, id := range f.src.served {
		if id == "data-dir" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("data-dir streamed %d times, want 2", count)
	}
}

func TestPausesAfterThreeFailures(t *testing.T) {
	f := newTargetFixture(t)
	f.restorer.failNext["volume-b"] = 3
	f.connectAndStart(t)
	p := waitPhase(t, f.target, PhasePaused)
	var b ItemProgress
	for _, it := range p.Items {
		if it.ID == "volume-b" {
			b = it
		}
	}
	if b.State != "failed" || b.Attempts != 3 || b.Error != "extract failed" {
		t.Fatalf("volume-b = %+v", b)
	}
	if err := f.target.Retry(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, f.target, PhaseFinalizing)
}

func TestTargetResumesFromStateFile(t *testing.T) {
	f := newTargetFixture(t)
	f.connectAndStart(t)
	waitPhase(t, f.target, PhaseFinalizing)

	// Rewind the state file to "two done, the third mid-stream".
	path := filepath.Join(f.deps.StateDir, "state.json")
	var st targetState
	raw, _ := os.ReadFile(path)
	json.Unmarshal(raw, &st)
	st.Phase = PhasePulling
	st.Items["volume-a"].State = "running"
	st.Items["volume-b"].State = "pending"
	raw, _ = json.Marshal(st)
	os.WriteFile(path, raw, 0o600)

	f.src.mu.Lock()
	f.src.served = nil
	f.src.mu.Unlock()
	f.restorer.cleaned = nil
	resumed := NewTarget(f.deps)
	if err := resumed.ResumeIfPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, resumed, PhaseFinalizing)
	f.src.mu.Lock()
	served := slices.Clone(f.src.served)
	f.src.mu.Unlock()
	if !slices.Equal(served, []string{"volume-a", "volume-b"}) {
		t.Fatalf("served after resume = %q, want only volume-a and volume-b", served)
	}
	if len(f.restorer.cleaned) == 0 || f.restorer.cleaned[0] != "volume-a" {
		t.Fatalf("cleaned = %q, want the interrupted item first", f.restorer.cleaned)
	}
}

func TestAbortCallsSourceAndDiscards(t *testing.T) {
	f := newTargetFixture(t)
	f.restorer.failNext["volume-b"] = 3
	f.connectAndStart(t)
	waitPhase(t, f.target, PhasePaused)
	if err := f.target.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.src.aborted {
		t.Fatal("the source was not told to abort")
	}
	if !slices.Contains(f.restorer.discarded, "volume-a") {
		t.Fatalf("discarded = %q", f.restorer.discarded)
	}
	if _, err := os.Stat(filepath.Join(f.deps.StateDir, "state.json")); !os.IsNotExist(err) {
		t.Fatal("state.json survived the abort")
	}
	if p := f.target.Progress(); p.Phase != PhaseIdle {
		t.Fatalf("phase after abort = %q", p.Phase)
	}
}

func TestStateFileMode0600(t *testing.T) {
	f := newTargetFixture(t)
	if _, err := f.target.Connect(context.Background(), f.src.srv.URL, "tok", "https://new"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(f.deps.StateDir, "state.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state.json = %v, %v", info, err)
	}
}

func TestResumeAtFinalizingCompletesAndFinishes(t *testing.T) {
	f := newTargetFixture(t)
	f.connectAndStart(t)
	waitPhase(t, f.target, PhaseFinalizing)
	restarted := NewTarget(f.deps)
	if err := restarted.ResumeIfPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := waitPhase(t, restarted, PhaseDone)
	if p.Summary == nil || p.Summary.DNS[0] != "shop.example.com" {
		t.Fatalf("summary = %+v", p.Summary)
	}
	f.src.mu.Lock()
	finished := f.src.finished
	f.src.mu.Unlock()
	if finished != "https://new.example.com" {
		t.Fatalf("finish sent %q", finished)
	}
	if _, err := os.Stat(filepath.Join(f.deps.StateDir, "state.json")); !os.IsNotExist(err) {
		t.Fatal("state.json survived completion")
	}
	if _, err := os.Stat(filepath.Join(f.deps.StateDir, "summary.json")); err != nil {
		t.Fatal("no summary.json")
	}
}
