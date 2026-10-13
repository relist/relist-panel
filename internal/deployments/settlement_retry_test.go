package deployments

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"relist-panel/internal/composes"
)

// A real SQLite failure leaves the completed Docker outcome unpersisted. The
// worker must retain that outcome and its active run instead of advancing FIFO.
func TestCompletionRetryHoldsWorkerUntilStored(t *testing.T) {
	h := newHarness(t)
	h.timeout = 250 * time.Millisecond
	a := h.enqueue(h.a, ModeDeploy)
	b := h.enqueue(h.b, ModeDeploy)
	blockCompletion(t, h.store.db, a.ID)
	trackStarts(t, h.store.db)
	h.comp.onDeploy = func(_ context.Context, id int64, _ composes.DeployOptions) ([]string, error) {
		if id == h.a {
			return []string{"preserve this warning"}, nil
		}
		return nil, nil
	}
	releases := h.rec.count("gate-")
	h.start()
	h.waitEvent("gate-", releases+1)
	// Wait beyond the old loop's retry delay and the execution timeout: neither
	// is permission to start B while A's already-known outcome is unpersisted.
	time.Sleep(1250 * time.Millisecond)
	if !h.q.IsRunning(h.a) {
		t.Fatal("completed deployment lost its active run before outcome persistence")
	}
	if got := h.get(h.b, b.Number).Status; got != StatusQueued {
		t.Fatalf("B status = %s during A completion failure, want queued", got)
	}
	if got := h.get(h.a, a.Number).Status; got != StatusRunning {
		t.Fatalf("A status = %s during completion failure, want running", got)
	}
	unblockCompletion(t, h.store.db)
	got := h.waitSettled(h.a, a.Number)
	if got.Status != StatusSucceeded || !reflect.DeepEqual(got.Warnings, []string{"preserve this warning"}) {
		t.Fatalf("retried outcome = %#v, want original success and warning", got)
	}
	if got := h.waitSettled(h.b, b.Number).Status; got != StatusSucceeded {
		t.Fatalf("B status = %s after A persisted, want succeeded", got)
	}
	if got := h.rec.count(fmt.Sprintf("deploy:%d", h.a)); got != 1 {
		t.Fatalf("A Docker deploy calls = %d, want 1", got)
	}
	var starts int
	if err := h.store.db.QueryRow(`SELECT COUNT(*) FROM completion_test_starts WHERE deployment_id = ?`, a.ID).Scan(&starts); err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatalf("A Start transitions = %d, want 1", starts)
	}
}

func TestCompletionRetryStopsWaitingOnShutdown(t *testing.T) {
	h := newHarness(t)
	a := h.enqueue(h.a, ModeDeploy)
	b := h.enqueue(h.b, ModeDeploy)
	blockCompletion(t, h.store.db, a.ID)
	releases := h.rec.count("gate-")
	h.start()
	h.waitEvent("gate-", releases+1)
	time.Sleep(50 * time.Millisecond)
	if !h.q.IsRunning(h.a) {
		t.Fatal("active run cleared while completion storage is unavailable")
	}
	h.cancel()
	select {
	case <-h.done:
	case <-time.After(time.Second):
		t.Fatal("worker waited indefinitely for unavailable completion storage on shutdown")
	}
	if got := h.get(h.a, a.Number).Status; got != StatusRunning {
		t.Fatalf("A status = %s despite failed persistence, want running", got)
	}
	if got := h.get(h.b, b.Number).Status; got != StatusQueued {
		t.Fatalf("B status = %s after shutdown, want queued", got)
	}
	if h.q.IsRunning(h.a) {
		t.Fatal("active run retained after worker shutdown")
	}
}

func TestUnstartedCompletionRetryHoldsWorkerUntilStored(t *testing.T) {
	h := newHarness(t)
	a := h.enqueue(h.a, ModeDeploy)
	b := h.enqueue(h.b, ModeDeploy)
	blockCompletion(t, h.store.db, a.ID)
	h.comp.gone[h.a] = true
	h.start()
	eventually(t, "unstarted deployment to become running", func() bool {
		return h.get(h.a, a.Number).Status == StatusRunning
	})
	time.Sleep(1250 * time.Millisecond)
	if got := h.get(h.b, b.Number).Status; got != StatusQueued {
		t.Fatalf("B status = %s during A completion failure, want queued", got)
	}
	unblockCompletion(t, h.store.db)
	got := h.waitSettled(h.a, a.Number)
	if got.Status != StatusCanceled || got.Reason != "compose deleted" {
		t.Fatalf("unstarted outcome = %#v, want canceled compose deleted", got)
	}
	if got := h.waitSettled(h.b, b.Number).Status; got != StatusSucceeded {
		t.Fatalf("B status = %s after A persisted, want succeeded", got)
	}
}

func TestCompletionRetryKeepsCancelWaitingUntilStored(t *testing.T) {
	h := newHarness(t)
	a := h.enqueue(h.a, ModeDeploy)
	blockCompletion(t, h.store.db, a.ID)
	h.gate.onAcquire = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	acquires := h.rec.count("gate+")
	h.start()
	h.waitEvent("gate+", acquires+1)
	canceled := make(chan error, 1)
	go func() { canceled <- h.q.Cancel(context.Background(), h.a, a.Number) }()
	h.waitEvent("gate-failed", 1)
	select {
	case err := <-canceled:
		t.Fatalf("Cancel returned before its outcome was persisted: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	unblockCompletion(t, h.store.db)
	select {
	case err := <-canceled:
		if err != nil {
			t.Fatalf("Cancel after storage recovered: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Cancel did not finish after its outcome was persisted")
	}
	if got := h.get(h.a, a.Number).Status; got != StatusCanceled {
		t.Fatalf("stored cancellation status = %s, want canceled", got)
	}
	if got := h.rec.count("deploy:"); got != 0 {
		t.Fatalf("Docker deploy calls after cancellation before gate = %d, want 0", got)
	}
}

func TestFinishRetryRestoresOriginalOutcome(t *testing.T) {
	db, compose, _ := newDB(t)
	store := NewStore(db)
	d := enqueue(t, store, compose, t0)
	if err := store.Start(context.Background(), d.ID, []string{"deploy"}, t0); err != nil {
		t.Fatal(err)
	}
	blockCompletion(t, db, d.ID)
	observed := &observedCompletionStore{store: store, attempted: make(chan error, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- finishWithRetry(ctx, observed, d.ID, outcome{
			status: StatusFailed, failedStep: "deploy", reason: "Docker failed", warnings: []string{"volume was removed"},
		}, time.Unix(1050, 0))
	}()
	select {
	case err := <-observed.attempted:
		if err == nil || !strings.Contains(err.Error(), "completion storage unavailable") {
			t.Fatalf("initial SQLite error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("completion write was not attempted")
	}
	unblockCompletion(t, db)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("completion stopped before retrying recovered SQLite: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("completion was not retried after SQLite recovered")
	}
	got, err := store.Get(context.Background(), compose, d.Number)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed || got.FailedStep != "deploy" || got.Reason != "Docker failed" ||
		!reflect.DeepEqual(got.Warnings, []string{"volume was removed"}) || got.FinishedAt == nil || got.FinishedAt.Unix() != 1050 {
		t.Fatalf("persisted outcome = %#v, want original failure, warning and completion time", got)
	}
}

func TestFinishRetryAttemptsOnceAfterCanceledContext(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(fmt.Sprintf("storageBlocked=%v", blocked), func(t *testing.T) {
			db, compose, _ := newDB(t)
			store := NewStore(db)
			d := enqueue(t, store, compose, t0)
			if err := store.Start(context.Background(), d.ID, []string{"deploy"}, t0); err != nil {
				t.Fatal(err)
			}
			if blocked {
				blockCompletion(t, db, d.ID)
			}
			observed := &observedCompletionStore{store: store, attempted: make(chan error, 4)}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := finishWithRetry(ctx, observed, d.ID, outcome{status: StatusSucceeded}, t0)
			if blocked && (err == nil || !strings.Contains(err.Error(), "completion storage unavailable")) {
				t.Fatalf("failed shutdown write = %v, want SQLite error", err)
			}
			if !blocked && err != nil {
				t.Fatalf("healthy shutdown write = %v", err)
			}
			if got := observed.calls.Load(); got != 1 {
				t.Fatalf("completion attempts after shutdown = %d, want 1", got)
			}
			got, err := store.Get(context.Background(), compose, d.Number)
			if err != nil {
				t.Fatal(err)
			}
			want := StatusSucceeded
			if blocked {
				want = StatusRunning
			}
			if got.Status != want {
				t.Fatalf("stored status = %s, want %s", got.Status, want)
			}
		})
	}
}

func TestFinishRetryStopsAfterShutdownDuringBackoff(t *testing.T) {
	db, compose, _ := newDB(t)
	store := NewStore(db)
	d := enqueue(t, store, compose, t0)
	if err := store.Start(context.Background(), d.ID, []string{"deploy"}, t0); err != nil {
		t.Fatal(err)
	}
	blockCompletion(t, db, d.ID)
	observed := &observedCompletionStore{store: store, attempted: make(chan error, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- finishWithRetry(ctx, observed, d.ID, outcome{status: StatusSucceeded}, t0)
	}()
	select {
	case <-observed.attempted:
	case <-time.After(time.Second):
		t.Fatal("completion write was not attempted")
	}
	select {
	case err := <-result:
		t.Fatalf("completion returned before shutdown instead of waiting for storage: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "completion storage unavailable") {
			t.Fatalf("shutdown error = %v, want original SQLite error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("completion wait did not return on shutdown")
	}
	if got := observed.calls.Load(); got != 1 {
		t.Fatalf("completion attempts after shutdown during backoff = %d, want 1", got)
	}
}

func TestFinishRetryDeletedRowSettlesQuietly(t *testing.T) {
	db, compose, _ := newDB(t)
	store := NewStore(db)
	d := enqueue(t, store, compose, t0)
	if err := store.Start(context.Background(), d.ID, []string{"deploy"}, t0); err != nil {
		t.Fatal(err)
	}
	blockCompletion(t, db, d.ID)
	observed := &observedCompletionStore{store: store, attempted: make(chan error, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- finishWithRetry(ctx, observed, d.ID, outcome{status: StatusSucceeded}, t0)
	}()
	select {
	case err := <-observed.attempted:
		if err == nil {
			t.Fatal("initial completion unexpectedly bypassed SQLite failure")
		}
	case <-time.After(time.Second):
		t.Fatal("completion write was not attempted")
	}
	if _, err := db.Exec(`DELETE FROM deployments WHERE id = ?`, d.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("finish deleted row = %v, want nil for zero affected rows", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("completion did not stop retrying after its row was deleted")
	}
}

type observedCompletionStore struct {
	store     *Store
	attempted chan error
	calls     atomic.Int64
}

func (s *observedCompletionStore) Finish(ctx context.Context, id int64, status Status, step, reason string, warnings []string, at time.Time) error {
	s.calls.Add(1)
	err := s.store.Finish(ctx, id, status, step, reason, warnings, at)
	select {
	case s.attempted <- err:
	default:
	}
	return err
}

func blockCompletion(t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	_, err := db.Exec(fmt.Sprintf(`CREATE TRIGGER completion_test_failure
BEFORE UPDATE OF status ON deployments
WHEN OLD.id = %d AND OLD.status = 'running' AND NEW.status <> 'running'
BEGIN SELECT RAISE(FAIL, 'completion storage unavailable'); END`, id))
	if err != nil {
		t.Fatal(err)
	}
}

func unblockCompletion(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`DROP TRIGGER completion_test_failure`); err != nil {
		t.Fatal(err)
	}
}

func trackStarts(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE completion_test_starts (deployment_id INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER completion_test_start AFTER UPDATE OF status ON deployments
WHEN OLD.status = 'queued' AND NEW.status = 'running'
BEGIN INSERT INTO completion_test_starts (deployment_id) VALUES (NEW.id); END`); err != nil {
		t.Fatal(err)
	}
}
