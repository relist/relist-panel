package composes

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"relist-panel/internal/builders"
)

const builderPath = "/api/projects/7/environments/3/composes/1/builder"

func seedBuilder(t *testing.T, store *builders.Store, name string) builders.Builder {
	t.Helper()
	b, err := store.Create(context.Background(), builders.Builder{Name: name, CPUsMilli: 1000, MemoryMB: 1024}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCaptureRecordsResolvedBuilder(t *testing.T) {
	ctx := context.Background()
	svc, _, target, db := newDeployService(t, "services:\n  web: {build: ./src}\n")
	store := builders.NewStore(db)
	svc.WithBuilders(store)
	b := seedBuilder(t, store, "large")
	captured, err := svc.Capture(ctx, target)
	if err != nil || captured.Snapshot.BuilderID != builders.DockerDefault {
		t.Fatalf("no choice: BuilderID = %d, %v", captured.Snapshot.BuilderID, err)
	}
	if err := store.SetDefault(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	captured, err = svc.Capture(ctx, target)
	if err != nil || captured.Snapshot.BuilderID != b.ID {
		t.Fatalf("panel default: BuilderID = %d, %v; want %d", captured.Snapshot.BuilderID, err, b.ID)
	}
}

func TestStageSnapshotCarriesBuilder(t *testing.T) {
	ctx := context.Background()
	svc, _, target, _ := newDeployService(t, "services:\n  web: {build: ./src}\n")
	captured, err := svc.Capture(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	captured.Request.Builder = "relist-builder-7"
	plan, err := svc.PrepareBuild(ctx, captured)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	if plan.Request.Builder != "relist-builder-7" {
		t.Fatalf("staged Builder = %q", plan.Request.Builder)
	}
}

func TestComposeBuilderRoutes(t *testing.T) {
	svc, _, _, db := newDeployService(t, "services:\n  web: {build: ./src}\n")
	store := builders.NewStore(db)
	svc.WithBuilders(store)
	b := seedBuilder(t, store, "large")
	rec := serve(t, svc, http.MethodGet, builderPath, "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"mode":"default","builderId":0}` {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body)
	}
	body := `{"mode":"builder","builderId":` + "1" + `}`
	if rec := serve(t, svc, http.MethodPut, builderPath, body); rec.Code != http.StatusNoContent {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
	rec = serve(t, svc, http.MethodGet, builderPath, "")
	if strings.TrimSpace(rec.Body.String()) != `{"mode":"builder","builderId":1}` {
		t.Fatalf("GET after PUT = %s (builder %d)", rec.Body, b.ID)
	}
	for _, bad := range []string{`{"mode":"builder","builderId":9}`, `{"mode":"nope"}`} {
		if rec := serve(t, svc, http.MethodPut, builderPath, bad); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("PUT %s = %d %s", bad, rec.Code, rec.Body)
		}
	}
	if rec := serve(t, svc, http.MethodGet, "/api/projects/7/environments/3/composes/2/builder", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET unknown compose = %d", rec.Code)
	}
}

func TestComposeBuilderRoutesNeedBuilders(t *testing.T) {
	svc, _, _, _ := newDeployService(t, "services:\n  web: {build: ./src}\n")
	if rec := serve(t, svc, http.MethodGet, builderPath, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET without builders = %d", rec.Code)
	}
}
