package chunkupload

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecoverInterruptsConsumingAndTruncates(t *testing.T) {
	h := newHarness(t)
	consuming := h.fullUpload()
	h.exec(`UPDATE uploads SET state = 'consuming' WHERE id = ?`, consuming.ID)
	uploading := h.mustCreate(1, 2, 5)
	h.exec(`UPDATE uploads SET received = 2 WHERE id = ?`, uploading.ID)
	if err := os.WriteFile(h.svc.StagedPath(uploading.ID), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := h.svc.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, _ := h.svc.Get(context.Background(), 1, 1, consuming.ID)
	if got.State != StateFailed || got.ErrorKind != KindInterrupted || got.Error == "" {
		t.Fatalf("consuming after recover = %+v", got)
	}
	if h.staged(uploading.ID) != "he" {
		t.Fatalf("uploading staged %q, want truncated to 2 bytes", h.staged(uploading.ID))
	}
}

func TestSweep(t *testing.T) {
	h := newHarness(t)
	old := h.now
	exists := func(id string) bool {
		_, err := getByID(context.Background(), h.db, id)
		return !errors.Is(err, ErrNotFound)
	}
	at := func(id string, ago time.Duration) {
		h.exec(`UPDATE uploads SET updated_at = ? WHERE id = ?`, old.Add(-ago).Unix(), id)
	}

	failedOld := h.fullUpload()
	h.exec(`UPDATE uploads SET state = 'failed' WHERE id = ?`, failedOld.ID)
	at(failedOld.ID, 25*time.Hour)

	emptyStale := h.mustCreate(1, 2, 5)
	at(emptyStale.ID, 2*time.Hour)

	startedStale := h.mustCreate(1, 3, 5)
	h.exec(`UPDATE uploads SET received = 1 WHERE id = ?`, startedStale.ID)
	at(startedStale.ID, 2*time.Hour)

	consumingOld := h.mustCreate(2, 1, 5)
	h.exec(`UPDATE uploads SET state = 'consuming' WHERE id = ?`, consumingOld.ID)
	at(consumingOld.ID, 48*time.Hour)

	missingFile := h.mustCreate(2, 2, 5)
	if err := os.Remove(h.svc.StagedPath(missingFile.ID)); err != nil {
		t.Fatal(err)
	}

	doneOld := h.mustCreate(2, 3, 5)
	h.exec(`UPDATE uploads SET state = 'done' WHERE id = ?`, doneOld.ID)
	_ = os.Remove(h.svc.StagedPath(doneOld.ID))
	at(doneOld.ID, 25*time.Hour)

	stray := filepath.Join(h.dataDir, ".uploads", "abc")
	if err := os.WriteFile(stray, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := h.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		id   string
		kept bool
	}{
		"failed 25h old":          {failedOld.ID, false},
		"empty 2h old":            {emptyStale.ID, false},
		"started 2h old":          {startedStale.ID, true},
		"consuming 48h old":       {consumingOld.ID, true},
		"uploading, file missing": {missingFile.ID, false},
		"done 25h old":            {doneOld.ID, false},
	} {
		if exists(tc.id) != tc.kept {
			t.Errorf("%s: row kept = %v, want %v", name, !tc.kept, tc.kept)
		}
		if !tc.kept {
			if _, err := os.Stat(h.svc.StagedPath(tc.id)); !os.IsNotExist(err) {
				t.Errorf("%s: staged file kept", name)
			}
		}
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Error("stray staged file kept")
	}
	if _, err := os.Stat(h.svc.StagedPath(startedStale.ID)); err != nil {
		t.Errorf("kept upload lost its file: %v", err)
	}
}

// A panel that was down longer than the expiry still offers the retry: Recover
// counts as activity, so the Sweep right after it keeps the upload.
func TestRecoverKeepsLongInterruptedUploads(t *testing.T) {
	h := newHarness(t)
	s := h.fullUpload()
	h.exec(`UPDATE uploads SET state = 'consuming', updated_at = ? WHERE id = ?`, h.now.Add(-48*time.Hour).Unix(), s.ID)
	if err := h.svc.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := h.svc.Get(context.Background(), 1, 1, s.ID)
	if err != nil || got.ErrorKind != KindInterrupted {
		t.Fatalf("after recover and sweep = %+v, %v", got, err)
	}
	if h.staged(s.ID) != "hello" {
		t.Fatal("staged file lost")
	}
}

// The sweep leaves an upload alone while a request is working on it.
func TestSweepSkipsABusyUpload(t *testing.T) {
	h := newHarness(t)
	s := h.mustCreate(1, 1, 5)
	h.exec(`UPDATE uploads SET updated_at = ? WHERE id = ?`, h.now.Add(-2*time.Hour).Unix(), s.ID)
	unlock, ok := h.svc.lock(s.ID)
	if !ok {
		t.Fatal("lock")
	}
	if err := h.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	unlock()
	if _, err := h.svc.Get(context.Background(), 1, 1, s.ID); err != nil {
		t.Fatalf("busy upload swept: %v", err)
	}
}

// The sweep decides from a snapshot. A row that changed since — completed and
// now being written — is not removed, and neither is its file.
func TestSweepRowLeavesAChangedRow(t *testing.T) {
	h := newHarness(t)
	s := h.fullUpload()
	snapshot, err := getByID(context.Background(), h.db, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.exec(`UPDATE uploads SET state = 'consuming', updated_at = ? WHERE id = ?`, h.now.Add(time.Minute).Unix(), s.ID)
	h.svc.mu.Lock()
	removed, err := h.svc.sweepRow(context.Background(), snapshot)
	h.svc.mu.Unlock()
	if err != nil || removed {
		t.Fatalf("sweepRow = %v, %v; want the changed row kept", removed, err)
	}
	if h.staged(s.ID) != "hello" {
		t.Fatal("staged file removed")
	}
}
