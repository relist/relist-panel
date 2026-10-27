package deployments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"relist-panel/internal/composes"
	"relist-panel/internal/sources"
	"relist-panel/internal/sshkeys"
)

func TestEnqueuePersistsConfigurationAcrossQueueRestart(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	d := h.enqueue(h.a, ModeBuild)
	h.comp.mu.Lock()
	h.comp.yaml[h.a] = "edited after enqueue"
	h.comp.mu.Unlock()
	h.newQueue()
	h.start()
	h.waitSettled(h.a, d.Number)
	if len(h.comp.prepared) != 1 || h.comp.prepared[0] != "yaml-a" {
		t.Fatalf("build used mutable config: %v", h.comp.prepared)
	}
}

type serialSnapshotGate struct {
	token    chan struct{}
	attempts chan struct{}
}

func (g serialSnapshotGate) Acquire(ctx context.Context, _ int64) (func(), error) {
	if g.attempts != nil {
		g.attempts <- struct{}{}
	}
	select {
	case g.token <- struct{}{}:
		return func() { <-g.token }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (g serialSnapshotGate) AcquireVolumes(context.Context, int64) (func(), error) {
	return func() {}, nil
}

func TestConcurrentDedupSkipsRedundantFailedSourceCapture(t *testing.T) {
	firstRequest := Request{Mode: ModeBuild, FreshVolumes: true, NoCache: true}
	for _, tc := range []struct {
		name     string
		second   Request
		conflict bool
	}{
		{"same request is deduplicated", firstRequest, false},
		{"other options are refused", Request{Mode: ModeBuild}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.gitSource(h.a)
			h.newQueue()
			gateAttempts := make(chan struct{}, 2)
			h.q.d.Gate = serialSnapshotGate{token: make(chan struct{}, 1), attempts: gateAttempts}
			lookupStarted, releaseLookup := make(chan struct{}), make(chan struct{})
			lookups := 0
			h.fetch.onResolve = func(context.Context, sources.Source, string) (string, error) {
				lookups++
				if lookups > 1 {
					return "", errors.New("redundant lookup failed")
				}
				close(lookupStarted)
				<-releaseLookup
				return "abc123", nil
			}
			type result struct {
				deployment Deployment
				created    bool
				err        error
			}
			first, second := make(chan result, 1), make(chan result, 1)
			go func() {
				d, c, e := h.q.Enqueue(context.Background(), h.a, firstRequest)
				first <- result{d, c, e}
			}()
			recv(t, lookupStarted, "first capture lookup")
			recv(t, gateAttempts, "first capture gate")
			secondStarted := make(chan struct{})
			go func() {
				close(secondStarted)
				d, c, e := h.q.Enqueue(context.Background(), h.a, tc.second)
				second <- result{d, c, e}
			}()
			recv(t, secondStarted, "second enqueue")
			recv(t, gateAttempts, "second capture waiting on gate")
			close(releaseLookup)
			a, b := <-first, <-second
			var conflict *QueuedConflictError
			switch {
			case a.err != nil || !a.created || lookups != 1:
				t.Fatalf("first=%+v lookups=%d", a, lookups)
			case tc.conflict && (!errors.As(b.err, &conflict) || b.created || conflict.Queued.ID != a.deployment.ID):
				t.Fatalf("second=%+v, want a QueuedConflictError for the first", b)
			case !tc.conflict && (b.err != nil || b.created || b.deployment.ID != a.deployment.ID || !b.deployment.FreshVolumes || !b.deployment.NoCache):
				t.Fatalf("dedup first=%+v second=%+v lookups=%d", a, b, lookups)
			}
		})
	}
}

func TestDeploymentLogAndHistoryRedactSnapshotSecrets(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	key, err := sshkeys.NewStore(h.db).Create(context.Background(), "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	// One body line of the key, which must not reach any log or history.
	secretLine := strings.Split(key.PrivateKey, "\n")[1]
	if err := h.srcs.Save(context.Background(), sources.Source{ComposeID: h.a, Kind: sources.KindGit, RepoURL: "git@example.com:a/b", Branch: "main", SSHKeyID: &key.ID}); err != nil {
		t.Fatal(err)
	}
	h.comp.snapshotSecrets = []string{"never-expose-token"}
	h.fetch.onFetch = func(_ context.Context, src sources.Source, _ string, out io.Writer) (sources.Ref, error) {
		fmt.Fprint(out, "never-expose-")
		fmt.Fprintln(out, "token")
		fmt.Fprintln(out, src.PrivateKey)
		return sources.Ref{SHA: "abc123"}, nil
	}
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	got := h.waitSettled(h.a, d.Number)
	content, _, err := ReadLog(LogPath(h.comp.dirs[h.a], d.Number), 0, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(toJSON(got))
	if strings.Contains(content, "never-expose") || strings.Contains(string(raw), "never-expose") ||
		strings.Contains(content, secretLine) || strings.Contains(string(raw), secretLine) {
		t.Fatalf("secret exposed: log=%q history=%s", content, raw)
	}
	mux := http.NewServeMux()
	api := NewAPI(h.q, nil)
	api.RegisterRoutes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/deployments", nil))
	if strings.Contains(response.Body.String(), "never-expose") || strings.Contains(response.Body.String(), secretLine) {
		t.Fatalf("global history leaked snapshot: %s", response.Body.String())
	}
}

func TestZIPSnapshotSurvivesReplacement(t *testing.T) {
	h := newHarness(t)
	h.zipSource(h.a)
	dir := h.comp.dirs[h.a]
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "index.txt"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := h.enqueue(h.a, ModeBuild)
	if err := os.WriteFile(filepath.Join(dir, "src", "index.txt"), []byte("replacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	var original bool
	h.comp.onDeploy = func(_ context.Context, _ int64, _ composes.DeployOptions) ([]string, error) {
		var raw string
		if err := h.store.db.QueryRow(`SELECT snapshot FROM deployment_snapshots WHERE deployment_id = ?`, d.ID).Scan(&raw); err != nil {
			t.Error(err)
			return nil, nil
		}
		var snap executionSnapshot
		if err := decodeSnapshot(raw, &snap); err != nil {
			t.Error(err)
			return nil, nil
		}
		content, err := os.ReadFile(filepath.Join(snap.SourceDir, "src", "index.txt"))
		original = err == nil && string(content) == "original"
		return nil, nil
	}
	h.newQueue()
	h.start()
	h.waitSettled(h.a, d.Number)
	if !original {
		t.Fatal("immutable uploaded Source was lost after replacement/restart")
	}
}

func TestSnapshotIsPrivateAndDedupKeepsFirstInputs(t *testing.T) {
	h := newHarness(t)
	h.newQueue()
	first, _, err := h.q.Enqueue(context.Background(), h.a, Request{Mode: ModeDeploy, FreshVolumes: true})
	if err != nil {
		t.Fatal(err)
	}
	h.comp.mu.Lock()
	h.comp.yaml[h.a] = "secret-yaml-after-enqueue"
	h.comp.mu.Unlock()
	second, created, err := h.q.Enqueue(context.Background(), h.a, Request{Mode: ModeDeploy, FreshVolumes: true})
	if err != nil || created || second.ID != first.ID || !second.FreshVolumes {
		t.Fatalf("dedup = %+v %v %v", second, created, err)
	}
	var raw string
	if err := h.store.db.QueryRow(`SELECT snapshot FROM deployment_snapshots WHERE deployment_id = ?`, first.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "yaml-a") || strings.Contains(raw, "secret-yaml-after-enqueue") {
		t.Fatalf("wrong snapshot: %s", raw)
	}
	if got := toJSON(second); strings.Contains(got.Reason, "secret-yaml") {
		t.Fatalf("snapshot leaked: %+v", got)
	}
}

func TestLegacyQueuedRowCapturesFallbackAndDisclosesIt(t *testing.T) {
	h := newHarness(t)
	d := enqueue(t, h.store, h.a, t0)
	h.comp.yaml[h.a] = "configuration at recovery"
	h.start()
	finished := h.waitSettled(h.a, d.Number)
	if finished.Status != StatusSucceeded || len(finished.Warnings) != 1 || !strings.Contains(finished.Warnings[0], "before execution snapshots") {
		t.Fatalf("legacy fallback not disclosed: %+v", finished)
	}
	snapshot, err := h.store.snapshot(context.Background(), d.ID)
	if err != nil || snapshot == nil || !snapshot.Legacy || snapshot.Target.Compose.YAMLContent != "configuration at recovery" {
		t.Fatalf("legacy snapshot=%+v err=%v", snapshot, err)
	}
	h.comp.mu.Lock()
	h.comp.yaml[h.a] = "later config"
	h.comp.mu.Unlock()
	retained, err := h.store.snapshot(context.Background(), d.ID)
	if err != nil || retained.Target.Compose.YAMLContent != "configuration at recovery" {
		t.Fatalf("fallback replaced after edit: %+v %v", retained, err)
	}
}

func TestQueuedCancelRemovesUnusedSourceCopy(t *testing.T) {
	h := newHarness(t)
	h.zipSource(h.a)
	d := h.enqueue(h.a, ModeBuild)
	snapshot, err := h.store.snapshot(context.Background(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.q.Cancel(context.Background(), h.a, d.Number); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snapshot.SourceDir); !os.IsNotExist(err) {
		t.Fatalf("canceled unused Source retained:%v", err)
	}
	if got := h.get(h.a, d.Number); got.Status != StatusCanceled {
		t.Fatalf("cancel history lost:%+v", got)
	}
}

func TestLegacySkipBuildCannotDeployAfterSourceRemoved(t *testing.T) {
	h := newHarness(t)
	d, _, err := h.store.Enqueue(context.Background(), h.a, ModeSkipBuild, false, false, t0)
	if err != nil {
		t.Fatal(err)
	}
	h.start()
	finished := h.waitSettled(h.a, d.Number)
	if finished.Status != StatusFailed || finished.Reason != "compose has no source" || len(h.comp.deployed) != 0 {
		t.Fatalf("legacy Source requirement lost:%+v", finished)
	}
}

func TestSkipBuildRejectsMissingSavedMaterialization(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	src, err := h.srcs.Get(context.Background(), h.a)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.srcs.RecordMaterialization(context.Background(), src, filepath.Join(h.comp.dirs[h.a], "retained", "src")); err != nil {
		t.Fatal(err)
	}
	h.newQueue()
	if _, _, err := h.q.Enqueue(context.Background(), h.a, Request{Mode: ModeSkipBuild}); err == nil {
		t.Fatal("missing saved materialization accepted as an empty Source")
	}
	rows, err := h.store.List(context.Background(), h.a, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("failed capture queued%d Deployments", len(rows))
	}
	if _, err := os.Stat(filepath.Join(h.comp.dirs[h.a], sourceSnapshotsDir)); !os.IsNotExist(err) {
		t.Fatalf("failed capture left private Source files:%v", err)
	}
}

func TestMissingChildDuringSourceCopyIsNotAnUnfetchedRoot(t *testing.T) {
	src := sources.Source{Kind: sources.KindGit}
	childErr := &os.PathError{Op: "lstat", Path: "/canonical/src/file", Err: os.ErrNotExist}
	if unfetchedCanonicalSource(src, "/canonical/src", childErr) {
		t.Fatal("missing mid-copy file was accepted as an unfetched canonical root")
	}
	rootErr := &os.PathError{Op: "lstat", Path: "/canonical/src", Err: os.ErrNotExist}
	if !unfetchedCanonicalSource(src, "/canonical/src", rootErr) {
		t.Fatal("genuinely unfetched canonical Source was rejected")
	}
}
