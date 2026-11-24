package chunkupload

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/database"
)

const gib = int64(1) << 30

// harness is one migrated database with two Users and three Composes, and a
// Service over a temp data dir whose free space the test scripts.
type harness struct {
	t       *testing.T
	db      *sql.DB
	dataDir string
	free    uint64
	now     time.Time
	svc     *Service
	cancel  context.CancelFunc
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dataDir := t.TempDir()
	db, err := database.OpenAndMigrate(filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range []string{
		`INSERT INTO users (id, email, created_at) VALUES (1, 'a@example.com', 1), (2, 'b@example.com', 1)`,
		`INSERT INTO projects (id, name, description, created_at, updated_at) VALUES (1, 'Shop', '', 1, 1)`,
		`INSERT INTO environments (id, project_id, name, created_at) VALUES (1, 1, 'prod', 1)`,
		`INSERT INTO composes (id, environment_id, name, yaml_content, created_at, updated_at) VALUES (1, 1, 'web', '', 1, 1), (2, 1, 'db', '', 1, 1), (3, 1, 'cache', '', 1, 1), (4, 1, 'queue', '', 1, 1)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	h := &harness{t: t, db: db, dataDir: dataDir, free: uint64(100 * gib), now: time.Unix(1_700_000_000, 0)}
	h.start()
	return h
}

// start (re)builds the Service, as a panel start does.
func (h *harness) start() {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	svc, err := New(ctx, h.db, h.dataDir, Options{
		Purposes:  []Purpose{{Name: "volume", Cap: 10 * gib}},
		FreeBytes: func(string) (uint64, error) { return h.free, nil },
		Now:       func() time.Time { return h.now },
	})
	if err != nil {
		cancel()
		h.t.Fatal(err)
	}
	h.svc, h.cancel = svc, cancel
	h.t.Cleanup(func() { cancel(); svc.Wait() })
}

func (h *harness) create(userID, composeID, size int64) (Session, error) {
	return h.svc.Create(context.Background(), NewSession{
		Purpose: "volume", UserID: userID, ComposeID: composeID,
		Target: "source=data&path=x.bin", Name: "x.bin", Size: size, LastModified: 42,
	})
}

func (h *harness) mustCreate(userID, composeID, size int64) Session {
	h.t.Helper()
	s, err := h.create(userID, composeID, size)
	if err != nil {
		h.t.Fatal(err)
	}
	return s
}

func (h *harness) exec(query string, args ...any) {
	h.t.Helper()
	if _, err := h.db.Exec(query, args...); err != nil {
		h.t.Fatal(err)
	}
}

func TestCreateStagesAnEmptyFile(t *testing.T) {
	h := newHarness(t)
	s := h.mustCreate(1, 1, 5)
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(s.ID) {
		t.Fatalf("id %q is not 32 hex chars", s.ID)
	}
	if s.Received != 0 || s.State != StateUploading || s.Size != 5 || s.Name != "x.bin" || s.LastModified != 42 {
		t.Fatalf("session = %+v", s)
	}
	info, err := os.Stat(h.svc.StagedPath(s.ID))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("staged size %d, want 0", info.Size())
	}
	if filepath.Dir(h.svc.StagedPath(s.ID)) != filepath.Join(h.dataDir, ".uploads") {
		t.Fatalf("staged under %s", h.svc.StagedPath(s.ID))
	}
}

func TestCreateRefusesOverCap(t *testing.T) {
	h := newHarness(t)
	if _, err := h.create(1, 1, 10*gib+1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over cap: %v, want ErrTooLarge", err)
	}
	if _, err := h.create(1, 1, 0); err == nil {
		t.Fatal("size 0 accepted")
	}
	if _, err := h.create(1, 1, 10*gib); err != nil {
		t.Fatalf("at cap: %v", err)
	}
}

func TestCreateRefusesFourthOpenUpload(t *testing.T) {
	h := newHarness(t)
	first := h.mustCreate(1, 1, 5)
	h.mustCreate(1, 2, 5)
	h.mustCreate(1, 3, 5)
	if _, err := h.create(1, 4, 5); !errors.Is(err, ErrTooManyOpen) {
		t.Fatalf("4th: %v, want ErrTooManyOpen", err)
	}
	// Another User has their own allowance.
	h.mustCreate(2, 4, 5)
	if err := h.svc.Cancel(context.Background(), 1, 1, first.ID); err != nil {
		t.Fatal(err)
	}
	h.mustCreate(1, 4, 5)
}

func TestCreateConcurrentRespectsCap(t *testing.T) {
	h := newHarness(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := h.create(1, int64(i%4)+1, 5); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if ok != 3 {
		t.Fatalf("%d concurrent creates succeeded, want 3", ok)
	}
}

func TestCreateReservesSpace(t *testing.T) {
	h := newHarness(t)
	h.free = uint64(10 * gib)
	first := h.mustCreate(1, 1, 6*gib)
	var noSpace *NoSpaceError
	if _, err := h.create(1, 2, 3*gib); !errors.As(err, &noSpace) {
		t.Fatalf("second: %v, want *NoSpaceError", err)
	}
	// Bytes already received are in `free`, not in the reservation.
	h.exec(`UPDATE uploads SET received = ? WHERE id = ?`, 6*gib, first.ID)
	h.free = uint64(4 * gib)
	if _, err := h.create(1, 2, 2*gib); err != nil {
		t.Fatalf("after received: %v", err)
	}
}

func TestGetHidesOtherUsersAndComposes(t *testing.T) {
	h := newHarness(t)
	s := h.mustCreate(1, 1, 5)
	if _, err := h.svc.Get(context.Background(), 1, 1, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Get(context.Background(), 2, 1, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user: %v", err)
	}
	if _, err := h.svc.Get(context.Background(), 1, 2, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other compose: %v", err)
	}
}

func TestCancelRemovesRowAndFile(t *testing.T) {
	h := newHarness(t)
	s := h.mustCreate(1, 1, 5)
	if err := h.svc.Cancel(context.Background(), 1, 1, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.svc.StagedPath(s.ID)); !os.IsNotExist(err) {
		t.Fatalf("staged file still there: %v", err)
	}
	if _, err := h.svc.Get(context.Background(), 1, 1, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row still there: %v", err)
	}
}

func TestCancelWhileConsumingRefused(t *testing.T) {
	h := newHarness(t)
	s := h.mustCreate(1, 1, 5)
	h.exec(`UPDATE uploads SET state = 'consuming' WHERE id = ?`, s.ID)
	if err := h.svc.Cancel(context.Background(), 1, 1, s.ID); !errors.Is(err, ErrConsuming) {
		t.Fatalf("cancel consuming: %v", err)
	}
}

func TestListOpenExcludesDone(t *testing.T) {
	h := newHarness(t)
	a := h.mustCreate(1, 1, 5)
	b := h.mustCreate(1, 1, 5)
	h.mustCreate(1, 2, 5) // another Compose
	h.mustCreate(2, 1, 5) // another User
	h.exec(`UPDATE uploads SET state = 'done' WHERE id = ?`, a.ID)
	list, err := h.svc.ListOpen(context.Background(), 1, 1, "volume")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != b.ID {
		t.Fatalf("list = %+v, want only %s", list, b.ID)
	}
}
