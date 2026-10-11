package deployments

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/database"
)

func newDB(t *testing.T) (*sql.DB, int64, int64) {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	exec := func(q string, args ...any) int64 {
		res, err := db.Exec(q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	p := exec(`INSERT INTO projects (name, created_at, updated_at) VALUES ('p', 1, 1)`)
	e := exec(`INSERT INTO environments (project_id, name, created_at) VALUES (?, 'e', 1)`, p)
	a := exec(`INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at) VALUES (?, 'alpha', '', 1, 1)`, e)
	b := exec(`INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at) VALUES (?, 'beta', '', 1, 1)`, e)
	return db, a, b
}

var t0 = time.Unix(1000, 0)

func enqueue(t *testing.T, s *Store, compose int64, at time.Time) Deployment {
	t.Helper()
	d, created, err := s.Enqueue(context.Background(), compose, ModeDeploy, false, false, at)
	if err != nil || !created {
		t.Fatalf("Enqueue: created=%v err=%v", created, err)
	}
	return d
}

func run(t *testing.T, s *Store, d Deployment, status Status, at time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := s.Start(ctx, d.ID, []string{"deploy"}, at); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Finish(ctx, d.ID, status, "", "", nil, at); err != nil {
		t.Fatalf("Finish: %v", err)
	}
}

func TestEnqueueNumbersPerCompose(t *testing.T) {
	db, a, b := newDB(t)
	s := NewStore(db)
	for want := 1; want <= 3; want++ {
		d := enqueue(t, s, a, t0)
		if d.Number != want || d.Status != StatusQueued {
			t.Fatalf("number = %d status %s, want %d queued", d.Number, d.Status, want)
		}
		run(t, s, d, StatusSucceeded, t0)
	}
	if d := enqueue(t, s, b, t0); d.Number != 1 {
		t.Fatalf("second compose number = %d, want 1", d.Number)
	}
}

func TestEnqueueReturnsExistingQueued(t *testing.T) {
	db, a, _ := newDB(t)
	s := NewStore(db)
	ctx := context.Background()
	first, created, err := s.Enqueue(ctx, a, ModeBuild, true, false, t0)
	if err != nil || !created {
		t.Fatalf("first: %v %v", created, err)
	}
	second, created, err := s.Enqueue(ctx, a, ModeSkipBuild, false, true, t0.Add(time.Second))
	if err != nil || created {
		t.Fatalf("second: created=%v err=%v", created, err)
	}
	if second.Number != first.Number || second.Mode != ModeBuild || !second.NoCache || second.FreshVolumes {
		t.Fatalf("second = %+v, want options of first", second)
	}
}

func TestEnqueueRaceReturnsWinner(t *testing.T) {
	db, a, _ := newDB(t)
	s := NewStore(db)
	const n = 10
	type result struct {
		d       Deployment
		created bool
		err     error
	}
	results := make(chan result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, created, err := s.Enqueue(context.Background(), a, ModeDeploy, false, false, t0)
			results <- result{d, created, err}
		}()
	}
	wg.Wait()
	close(results)
	creators, number := 0, 0
	for r := range results {
		if r.err != nil {
			t.Fatalf("Enqueue error: %v", r.err)
		}
		if r.created {
			creators++
		}
		if number == 0 {
			number = r.d.Number
		}
		if r.d.Number != number {
			t.Fatalf("numbers differ: %d vs %d", r.d.Number, number)
		}
	}
	if creators != 1 {
		t.Fatalf("creators = %d, want 1", creators)
	}
}

func TestNextQueuedIsFIFOAcrossComposes(t *testing.T) {
	db, a, b := newDB(t)
	s := NewStore(db)
	ctx := context.Background()
	if _, ok, err := s.NextQueued(ctx); err != nil || ok {
		t.Fatalf("empty NextQueued ok=%v err=%v", ok, err)
	}
	db1 := enqueue(t, s, b, t0)
	da := enqueue(t, s, a, t0.Add(time.Second))
	got, ok, err := s.NextQueued(ctx)
	if err != nil || !ok || got.ID != db1.ID {
		t.Fatalf("NextQueued = %+v ok=%v err=%v, want first enqueued", got, ok, err)
	}
	run(t, s, db1, StatusSucceeded, t0)
	got, ok, _ = s.NextQueued(ctx)
	if !ok || got.ID != da.ID {
		t.Fatalf("NextQueued = %+v, want second", got)
	}
}

func TestFailRunningMarksOnlyRunning(t *testing.T) {
	db, a, b := newDB(t)
	s := NewStore(db)
	ctx := context.Background()
	running := enqueue(t, s, a, t0)
	if err := s.Start(ctx, running.ID, []string{"fetch", "deploy"}, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStep(ctx, running.ID, "fetch"); err != nil {
		t.Fatal(err)
	}
	queued := enqueue(t, s, b, t0)
	n, err := s.FailRunning(ctx, "panel restarted", t0.Add(time.Minute))
	if err != nil || n != 1 {
		t.Fatalf("FailRunning = %d, %v", n, err)
	}
	r, _ := s.Get(ctx, a, running.Number)
	if r.Status != StatusFailed || r.Reason != "panel restarted" || r.FailedStep != "fetch" || r.FinishedAt == nil {
		t.Fatalf("running row = %+v", r)
	}
	if len(r.Steps) != 2 || r.Steps[0] != "fetch" {
		t.Fatalf("steps = %v", r.Steps)
	}
	q, _ := s.Get(ctx, b, queued.Number)
	if q.Status != StatusQueued || q.Reason != "" || q.FinishedAt != nil {
		t.Fatalf("queued row touched: %+v", q)
	}
}

func TestAheadCountsQueuedBeforeAndRunning(t *testing.T) {
	db, a, b := newDB(t)
	s := NewStore(db)
	ctx := context.Background()
	first := enqueue(t, s, a, t0)
	second := enqueue(t, s, b, t0) // same second: id breaks the tie
	if n, _ := s.Ahead(ctx, first); n != 0 {
		t.Fatalf("first ahead = %d, want 0", n)
	}
	if n, _ := s.Ahead(ctx, second); n != 1 {
		t.Fatalf("second ahead = %d, want 1", n)
	}
	if err := s.Start(ctx, first.ID, nil, t0); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.Ahead(ctx, second); n != 1 {
		t.Fatalf("second ahead with first running = %d, want 1", n)
	}
	third := enqueue(t, s, a, t0.Add(time.Second))
	if n, _ := s.Ahead(ctx, third); n != 2 {
		t.Fatalf("third ahead = %d, want 2 (one queued, one running)", n)
	}
}

func TestCancelQueued(t *testing.T) {
	db, a, _ := newDB(t)
	s := NewStore(db)
	ctx := context.Background()
	if ok, err := s.CancelQueued(ctx, a, t0); err != nil || ok {
		t.Fatalf("cancel none = %v %v", ok, err)
	}
	d := enqueue(t, s, a, t0)
	if ok, err := s.CancelQueued(ctx, a, t0); err != nil || !ok {
		t.Fatalf("cancel = %v %v", ok, err)
	}
	got, _ := s.Get(ctx, a, d.Number)
	if got.Status != StatusCanceled || got.FinishedAt == nil {
		t.Fatalf("got %+v", got)
	}
}

func TestFinishStoresWarningsAndListIsNewestFirst(t *testing.T) {
	db, a, _ := newDB(t)
	s := NewStore(db)
	ctx := context.Background()
	d1 := enqueue(t, s, a, t0)
	if err := s.Start(ctx, d1.ID, []string{"fetch"}, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSource(ctx, d1.ID, "abc", "subject"); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, d1.ID, StatusFailed, "fetch", "boom", []string{"w1"}, t0); err != nil {
		t.Fatal(err)
	}
	d2 := enqueue(t, s, a, t0)
	list, err := s.List(ctx, a, 10)
	if err != nil || len(list) != 2 || list[0].ID != d2.ID {
		t.Fatalf("List = %+v, %v", list, err)
	}
	got := list[1]
	if got.Status != StatusFailed || got.FailedStep != "fetch" || got.Reason != "boom" ||
		got.SourceRef != "abc" || got.SourceSubject != "subject" || len(got.Warnings) != 1 || got.StartedAt == nil {
		t.Fatalf("got %+v", got)
	}
	if limited, _ := s.List(ctx, a, 1); len(limited) != 1 {
		t.Fatalf("limit ignored: %d", len(limited))
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	db, a, _ := newDB(t)
	s := NewStore(db)
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		run(t, s, enqueue(t, s, a, t0), StatusSucceeded, t0)
	}
	removed, err := s.Prune(ctx, a, 20)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	want := []int{1, 2, 3, 4, 5}
	if len(removed) != len(want) {
		t.Fatalf("removed = %v, want %v", removed, want)
	}
	for i := range want {
		if removed[i] != want[i] {
			t.Fatalf("removed = %v, want %v", removed, want)
		}
	}
	list, _ := s.List(ctx, a, 100)
	if len(list) != 20 || list[19].Number != 6 {
		t.Fatalf("remaining = %d, oldest %d", len(list), list[len(list)-1].Number)
	}
}

func TestActiveListsQueuedAndRunningWithComposeName(t *testing.T) {
	db, a, b := newDB(t)
	s := NewStore(db)
	ctx := context.Background()
	done := enqueue(t, s, a, t0)
	run(t, s, done, StatusSucceeded, t0)
	queuedA := enqueue(t, s, a, t0)
	runningB := enqueue(t, s, b, t0.Add(time.Second))
	if err := s.Start(ctx, runningB.ID, nil, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStep(ctx, runningB.ID, "deploy"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Active(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("Active = %+v, %v", got, err)
	}
	if got[0].ComposeName != "beta" || got[0].Status != StatusRunning || got[0].Step != "deploy" || got[0].Number != 1 {
		t.Fatalf("got[0] = %+v", got[0])
	}
	if got[1].ComposeName != "alpha" || got[1].Status != StatusQueued || got[1].Number != queuedA.Number {
		t.Fatalf("got[1] = %+v", got[1])
	}
}
