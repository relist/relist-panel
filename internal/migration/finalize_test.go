package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/database"
	"relist-panel/internal/deployments"
	"relist-panel/internal/domains"
	"relist-panel/internal/sources"
)

// stageDatabase writes a migrated database with one marker Project into
// <dataDir>/.migration/staging/panel.db.
func stageDatabase(t *testing.T, dataDir string) {
	t.Helper()
	staging := filepath.Join(dataDir, ".migration", "staging")
	os.MkdirAll(staging, 0o700)
	db, err := database.OpenAndMigrate(filepath.Join(staging, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects (name, created_at, updated_at) VALUES ('from-the-old-host', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	os.WriteFile(filepath.Join(dataDir, ".migration", "finalize"), []byte(`{"runningComposeIds":[]}`), 0o600)
}

func freshDatabase(t *testing.T, dataDir string) {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
}

func markerProject(t *testing.T, dataDir string) bool {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM projects WHERE name = 'from-the-old-host'`).Scan(&n)
	return n == 1
}

func TestPrepareFinalizeRemovesJournalsBeforeSwap(t *testing.T) {
	dataDir := t.TempDir()
	freshDatabase(t, dataDir)
	for _, j := range []string{"panel.db-journal", "panel.db-wal", "panel.db-shm"} {
		os.WriteFile(filepath.Join(dataDir, j), []byte("stale"), 0o600)
	}
	stageDatabase(t, dataDir)
	pending, err := PrepareFinalize(dataDir)
	if err != nil || !pending {
		t.Fatalf("PrepareFinalize() = %v, %v", pending, err)
	}
	for _, j := range []string{"panel.db-journal", "panel.db-wal", "panel.db-shm"} {
		if _, err := os.Stat(filepath.Join(dataDir, j)); !os.IsNotExist(err) {
			t.Fatalf("%s survived the swap", j)
		}
	}
	if !markerProject(t, dataDir) {
		t.Fatal("panel.db is not the staged database")
	}
}

func TestPrepareFinalizeIsIdempotent(t *testing.T) {
	dataDir := t.TempDir()
	freshDatabase(t, dataDir)
	stageDatabase(t, dataDir)
	for i := 0; i < 2; i++ {
		if pending, err := PrepareFinalize(dataDir); err != nil || !pending {
			t.Fatalf("PrepareFinalize() #%d = %v, %v", i, pending, err)
		}
	}
	if !markerProject(t, dataDir) {
		t.Fatal("the second PrepareFinalize lost the database")
	}
}

func TestPrepareFinalizeAfterRenameStillPending(t *testing.T) {
	dataDir := t.TempDir()
	stageDatabase(t, dataDir)
	os.Rename(filepath.Join(dataDir, ".migration", "staging", "panel.db"), filepath.Join(dataDir, "panel.db"))
	pending, err := PrepareFinalize(dataDir)
	if err != nil || !pending {
		t.Fatalf("PrepareFinalize() = %v, %v; want pending", pending, err)
	}
	db := openDBAt(t, filepath.Join(dataDir, "panel.db"))
	NewStore(db, nil).SetLock(context.Background(), StepReady, LockState{})
	if err := ClearCopiedState(context.Background(), db, time.Now()); err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM migration_lock`).Scan(&n)
	if n != 0 {
		t.Fatal("the copied lock survived")
	}
}

func TestPrepareFinalizeWithoutMarker(t *testing.T) {
	if pending, err := PrepareFinalize(t.TempDir()); err != nil || pending {
		t.Fatalf("PrepareFinalize() = %v, %v; want not pending", pending, err)
	}
}

func TestClearCopiedStateClearsLockAndCancels(t *testing.T) {
	db := openDB(t)
	store := NewStore(db, nil)
	ctx := context.Background()
	store.CreateToken(ctx, time.Hour)
	store.SetLock(ctx, StepReady, LockState{})
	db.Exec(`INSERT INTO projects (name, created_at, updated_at) VALUES ('p', 1, 1)`)
	db.Exec(`INSERT INTO environments (project_id, name, created_at) VALUES (1, 'e', 1)`)
	db.Exec(`INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at) VALUES (1, 'a', '', 1, 1)`)
	if _, _, err := deployments.NewStore(db).Enqueue(ctx, 1, deployments.ModeDeploy, false, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := ClearCopiedState(ctx, db, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"migration_lock", "migration_token"} {
		var n int
		db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n)
		if n != 0 {
			t.Fatalf("%s has %d rows", table, n)
		}
	}
	var status, reason string
	db.QueryRow(`SELECT status, reason FROM deployments`).Scan(&status, &reason)
	if status != "canceled" || reason != "server migration" {
		t.Fatalf("deployment = %s %q", status, reason)
	}
}

type fakeEnqueuer struct {
	mu    sync.Mutex
	calls map[int64]deployments.Mode
}

func (q *fakeEnqueuer) Enqueue(_ context.Context, id int64, r deployments.Request) (deployments.Deployment, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.calls[id] = r.Mode
	return deployments.Deployment{ComposeID: id, Number: 7}, true, nil
}

type sourceSet map[int64]bool

func (s sourceSet) Get(_ context.Context, id int64) (sources.Source, error) {
	if s[id] {
		return sources.Source{ComposeID: id}, nil
	}
	return sources.Source{}, sql.ErrNoRows
}

func TestCompleteFinalizeEnqueuesModesPerSource(t *testing.T) {
	docker := NewFakeDocker()
	docker.TraefikUp = false
	queue := &fakeEnqueuer{calls: map[int64]deployments.Mode{}}
	f := NewFinalizer(FinalizeDeps{Local: LocalHost{Docker: docker, DataDir: t.TempDir()}, Queue: queue, Sources: sourceSet{1: true},
		Domains: fakeDomains{{Host: "shop.example.com"}, {Host: "shop.example.com", Path: "/api"}, {Host: "api.example.com"}}})
	summary, err := f.Complete(context.Background(), Manifest{RunningComposeIDs: []int64{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	if !docker.TraefikUp {
		t.Fatal("Traefik not started again")
	}
	if len(queue.calls) != 2 || queue.calls[1] != deployments.ModeSkipBuild || queue.calls[2] != deployments.ModeDeploy {
		t.Fatalf("enqueued = %v", queue.calls)
	}
	if !slices.Equal(summary.DNS, []string{"api.example.com", "shop.example.com"}) || len(summary.Deployments) != 2 || summary.Deployments[0].Number != 7 {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestBeginStopsTraefikBeforeMovingDataDir(t *testing.T) {
	dataDir := t.TempDir()
	docker := NewFakeDocker()
	staged := filepath.Join(dataDir, ".migration", "staging", "data", ".traefik")
	os.MkdirAll(staged, 0o700)
	os.WriteFile(filepath.Join(staged, "acme.json"), []byte("old"), 0o600)
	os.MkdirAll(filepath.Join(dataDir, ".traefik"), 0o700)
	os.WriteFile(filepath.Join(dataDir, ".traefik", "acme.json"), []byte("fresh"), 0o600)
	freshDatabase(t, dataDir)
	exited := false
	f := NewFinalizer(FinalizeDeps{Local: LocalHost{Docker: docker, DataDir: dataDir}, Restart: func() {
		exited = true
		if docker.TraefikUp {
			t.Error("exited with Traefik still running")
		}
	}})
	if err := f.Begin(context.Background(), Manifest{RunningComposeIDs: []int64{3}}); err != nil {
		t.Fatal(err)
	}
	if !exited {
		t.Fatal("Begin did not exit")
	}
	if b, _ := os.ReadFile(filepath.Join(dataDir, ".traefik", "acme.json")); string(b) != "old" {
		t.Fatalf("acme.json = %q, want the old host's", b)
	}
	var marker struct{ RunningComposeIDs []int64 }
	raw, _ := os.ReadFile(filepath.Join(dataDir, ".migration", "finalize"))
	json.Unmarshal(raw, &marker)
	if !slices.Equal(marker.RunningComposeIDs, []int64{3}) {
		t.Fatalf("marker = %s", raw)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "panel.db")); err != nil {
		t.Fatal("Begin touched the open database")
	}
}

var _ DomainLister = fakeDomains{}
var _ = domains.Domain{}

func TestBeginRestartsAfterTheMarker(t *testing.T) {
	dataDir := t.TempDir()
	freshDatabase(t, dataDir)
	os.MkdirAll(filepath.Join(dataDir, ".migration"), 0o700)
	calls := 0
	f := NewFinalizer(FinalizeDeps{Local: LocalHost{Docker: NewFakeDocker(), DataDir: dataDir}, Restart: func() {
		calls++
		if _, err := os.Stat(filepath.Join(dataDir, ".migration", "finalize")); err != nil {
			t.Errorf("restarted before the marker was written: %v", err)
		}
	}})
	if err := f.Begin(context.Background(), Manifest{}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("Restart called %d times, want 1", calls)
	}
}
