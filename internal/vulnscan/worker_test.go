package vulnscan

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestWorkerYieldsAndDedupes(t *testing.T) {
	h := newHarness(t)
	h.enable()
	h.gate.busy.Store(true)
	h.start()
	ctx := context.Background()
	for range 3 {
		if err := h.svc.ScanImage(ctx, "sha256:app", ReasonDeploy); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(30 * time.Millisecond) // several ticks while busy
	if n := h.scanner.count("scan sha256:app"); n != 0 {
		t.Fatalf("scanned %d times while a Deployment ran", n)
	}
	h.gate.busy.Store(false)
	eventually(t, "queue drained", h.queueEmpty)
	if n := h.scanner.count("scan sha256:app"); n != 1 {
		t.Fatalf("scanned %d times, want 1", n)
	}
	results, _ := h.store.ImageResults(ctx, []string{"sha256:app"})
	if r := results["sha256:app"]; r.ImageRef != "app:1" || r.ScannedAt == nil {
		t.Fatalf("result = %+v", r)
	}
}

func TestWorkerSkipsSameDB(t *testing.T) {
	h := newHarness(t)
	h.enable()
	ctx := context.Background()
	h.store.RecordScan(ctx, "sha256:app", "app:1", Report{}, "db1", t0)
	h.start()
	h.svc.ScanImage(ctx, "sha256:app", ReasonDaily)
	eventually(t, "queue drained", h.queueEmpty)
	if n := h.scanner.count("scan sha256:app"); n != 0 {
		t.Fatalf("automatic scan with an unchanged DB ran %d times", n)
	}
	h.svc.ScanImage(ctx, "sha256:app", ReasonManual)
	eventually(t, "manual scan", func() bool { return h.scanner.count("scan sha256:app") == 1 })
}

func TestWorkerNoDB(t *testing.T) {
	h := newHarness(t)
	h.enable()
	h.store.SetDB(context.Background(), "", t0, "") // clears the recorded version
	h.store.db.Exec(`UPDATE scan_settings SET db_version = NULL`)
	h.scanner.dbVersion = ""
	h.scanner.updateErr = errBoom // the DB update queued on start fails too
	h.start()
	h.svc.ScanImage(context.Background(), "sha256:app", ReasonManual)
	eventually(t, "queue drained", h.queueEmpty)
	_, errText, found, _ := h.store.ScanState(context.Background(), "sha256:app")
	if !found || errText != "vulnerability DB not downloaded yet" {
		t.Fatalf("error = %q (found %v)", errText, found)
	}
	if h.scanner.count("scan sha256:app") != 0 {
		t.Fatal("scanned without a DB")
	}
}

func TestWorkerImageGone(t *testing.T) {
	h := newHarness(t)
	h.enable()
	h.scanner.scanErr["sha256:app"] = ErrImageGone
	h.start()
	ctx := context.Background()
	h.svc.ScanImage(ctx, "sha256:app", ReasonManual)
	h.svc.ScanImage(ctx, "sha256:other", ReasonManual)
	eventually(t, "queue drained", h.queueEmpty)
	_, errText, _, _ := h.store.ScanState(ctx, "sha256:app")
	if errText != "image no longer exists" {
		t.Fatalf("error = %q", errText)
	}
	if h.scanner.count("scan sha256:other") != 1 || h.scanner.count("scan sha256:app") != 1 {
		t.Fatalf("calls = %v", h.scanner.snapshot())
	}
}

func TestWorkerRestartCleansAndRequeues(t *testing.T) {
	h := newHarness(t)
	h.enable()
	ctx := context.Background()
	h.store.Enqueue(ctx, KindImage, "sha256:app", ReasonManual, t0)
	task, _, _ := h.store.Next(ctx)
	h.store.Start(ctx, task.ID, t0) // a scan a restart interrupted
	h.start()
	eventually(t, "queue drained", h.queueEmpty)
	calls := h.scanner.snapshot()
	cleanup, scan := slices.Index(calls, "cleanup"), slices.Index(calls, "scan sha256:app")
	if cleanup < 0 || scan < 0 || cleanup > scan {
		t.Fatalf("calls = %v", calls)
	}
	if h.scanner.count("scan sha256:app") != 1 {
		t.Fatalf("calls = %v", calls)
	}
}

func TestRunEnsuresImageOnStart(t *testing.T) {
	h := newHarness(t)
	h.enable()
	h.store.SetActiveImage(context.Background(), "old/trivy@sha256:"+hex64)
	h.store.Enqueue(context.Background(), KindImage, "sha256:app", ReasonManual, t0)
	h.start()
	eventually(t, "queue drained", h.queueEmpty)
	calls := h.scanner.snapshot()
	ensure, scan := slices.Index(calls, "ensure "+DefaultScannerImage), slices.Index(calls, "scan sha256:app")
	if ensure < 0 || scan < ensure {
		t.Fatalf("calls = %v", calls)
	}
	if !slices.Contains(calls, "remove old/trivy@sha256:"+hex64) {
		t.Fatalf("old default not removed: %v", calls)
	}
	if !slices.Contains(calls, "scan sha256:scanner") {
		t.Fatalf("new scanner image not scanned: %v", calls)
	}
	st, _ := h.store.Settings(context.Background())
	if st.ActiveImage != DefaultScannerImage {
		t.Fatalf("active = %q", st.ActiveImage)
	}
}

func TestRunQueuesDBUpdateWhenMissing(t *testing.T) {
	h := newHarness(t)
	h.enable()
	h.store.db.Exec(`UPDATE scan_settings SET db_version = NULL`)
	h.scanner.dbVersion = ""
	h.start()
	eventually(t, "DB updated", func() bool { return h.scanner.count("update") == 1 })
	eventually(t, "queue drained", h.queueEmpty)
	st, _ := h.store.Settings(context.Background())
	if st.DBVersion != "db1" {
		t.Fatalf("DBVersion = %q", st.DBVersion)
	}
}

func TestDBUpdateFailureKeepsOldVersion(t *testing.T) {
	h := newHarness(t)
	h.enable()
	h.scanner.updateErr = errBoom
	h.start()
	h.store.Enqueue(context.Background(), KindDBUpdate, "", ReasonDaily, t0)
	h.svc.wakeUp()
	eventually(t, "queue drained", h.queueEmpty)
	st, _ := h.store.Settings(context.Background())
	if st.DBVersion != "db1" || st.DBError != "boom" {
		t.Fatalf("settings = %+v", st)
	}
}

func TestEnableFailsKeepsOff(t *testing.T) {
	h := newHarness(t)
	h.scanner.ensureErr = errors.New("pull denied")
	if err := h.svc.Enable(context.Background()); err == nil || err.Error() != "pull denied" {
		t.Fatalf("err = %v", err)
	}
	st, _ := h.store.Settings(context.Background())
	if st.Enabled {
		t.Fatal("enabled after a failed pull")
	}
	if !h.queueEmpty() {
		t.Fatal("queued work after a failed enable")
	}
}

func TestEnableQueuesSetup(t *testing.T) {
	h := newHarness(t)
	if err := h.svc.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, _ := h.store.Settings(context.Background())
	if !st.Enabled || st.ActiveImage != DefaultScannerImage {
		t.Fatalf("settings = %+v", st)
	}
	task, ok, _ := h.store.Next(context.Background())
	if !ok || task.Kind != KindDBUpdate || task.Reason != ReasonSetup {
		t.Fatalf("first task = %+v", task)
	}
	_, queued, _ := h.store.QueueState(context.Background())
	if queued != 4 { // db_update + app + other + scanner
		t.Fatalf("queued = %d", queued)
	}
}

func TestDisableClearsQueue(t *testing.T) {
	h := newHarness(t)
	h.enable()
	h.svc.ScanAll(context.Background(), ReasonManual)
	if err := h.svc.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, _ := h.store.Settings(context.Background())
	if st.Enabled || !h.queueEmpty() {
		t.Fatalf("settings %+v, queue empty %v", st, h.queueEmpty())
	}
	if err := h.svc.ScanImage(context.Background(), "sha256:app", ReasonManual); !errors.Is(err, ErrDisabled) {
		t.Fatalf("scan while off = %v", err)
	}
}

func TestSetScannerImageFailureKeepsOld(t *testing.T) {
	h := newHarness(t)
	h.enable()
	h.scanner.ensureErr = errors.New("manifest unknown")
	ref := "aquasec/trivy:9.9.9@sha256:" + hex64
	if err := h.svc.SetScannerImage(context.Background(), ref); err == nil {
		t.Fatal("want error")
	}
	st, _ := h.store.Settings(context.Background())
	if st.ScannerImage != "" || st.ActiveImage != DefaultScannerImage {
		t.Fatalf("settings = %+v", st)
	}
}

func TestSetScannerImageSwitches(t *testing.T) {
	h := newHarness(t)
	h.enable()
	ref := "aquasec/trivy:9.9.9@sha256:" + hex64
	if err := h.svc.SetScannerImage(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	st, _ := h.store.Settings(context.Background())
	if st.ScannerImage != ref || st.ActiveImage != ref {
		t.Fatalf("settings = %+v", st)
	}
	if h.scanner.count("remove "+DefaultScannerImage) != 1 {
		t.Fatalf("old image kept: %v", h.scanner.snapshot())
	}
	if _, ok, _ := h.store.Next(context.Background()); !ok {
		t.Fatal("new scanner image not queued")
	}
	if err := h.svc.SetScannerImage(context.Background(), "aquasec/trivy:latest"); !errors.Is(err, ErrInvalidScannerImage) {
		t.Fatalf("invalid ref = %v", err)
	}
}

func TestScanComposeRespectsSwitches(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.svc.ScanCompose(ctx, 3)
	if !h.queueEmpty() {
		t.Fatal("queued while off")
	}
	h.enable()
	st, _ := h.store.Settings(ctx)
	st.AfterDeploy = false
	h.store.SaveSettings(ctx, st)
	h.svc.ScanCompose(ctx, 3)
	if !h.queueEmpty() {
		t.Fatal("queued with after-deploy off")
	}
	st.AfterDeploy = true
	h.store.SaveSettings(ctx, st)
	if err := h.svc.ScanCompose(ctx, 3); err != nil {
		t.Fatal(err)
	}
	task, ok, _ := h.store.Next(ctx)
	_, queued, _ := h.store.QueueState(ctx)
	if !ok || task.ImageID != "sha256:app" || task.Reason != ReasonDeploy || queued != 1 {
		t.Fatalf("task = %+v, queued %d", task, queued)
	}
}

func TestUsedImagesAddsScanner(t *testing.T) {
	h := newHarness(t)
	used, err := h.svc.UsedImages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	last := used[len(used)-1]
	if len(used) != 3 || last.ImageID != "sha256:scanner" || last.Users[0].Kind != UserScanner {
		t.Fatalf("used = %s", fmt.Sprint(used))
	}
}

func TestWorkerShutdownKeepsTheRunningTask(t *testing.T) {
	h := newHarness(t)
	h.enable()
	h.scanner.blockScan, h.scanner.scanning = true, make(chan string, 1)
	h.start()
	h.svc.ScanImage(context.Background(), "sha256:app", ReasonManual)
	select {
	case <-h.scanner.scanning:
	case <-time.After(2 * time.Second):
		t.Fatal("scan did not start")
	}
	h.stop() // a graceful shutdown cancels the worker mid-scan
	if _, _, found, _ := h.store.ScanState(context.Background(), "sha256:app"); found {
		t.Fatal("an interrupted scan was recorded as a failure")
	}
	task, ok, _ := h.store.Next(context.Background())
	if !ok || task.ImageID != "sha256:app" || !task.Started {
		t.Fatalf("interrupted task lost: %+v %v", task, ok)
	}
	// The next start runs it again, once.
	h.scanner.blockScan = false
	h.svc = New(Deps{Store: h.store, Scanner: h.scanner, InUse: h.inUse, Gate: h.gate, Now: func() time.Time { return t0 }, Tick: 5 * time.Millisecond})
	h.start()
	eventually(t, "queue drained", h.queueEmpty)
	if n := h.scanner.count("scan sha256:app"); n != 2 {
		t.Fatalf("scan ran %d times, want 2 (interrupted + rerun)", n)
	}
}

func TestRunQueuesDBUpdateWhenTheCacheIsEmpty(t *testing.T) {
	h := newHarness(t)
	h.enable() // db_version is recorded, as after a Migration
	h.scanner.dbVersion = ""
	h.start()
	eventually(t, "DB updated", func() bool { return h.scanner.count("update") == 1 })
}
