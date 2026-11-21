package databases

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
)

// simulateOlderPanel makes Database id look as if an older panel, whose
// driver had another healthcheck, created it: its applied spec and its
// container both carry the old settings.
func simulateOlderPanel(t *testing.T, r *rig, id int64) ContainerSpec {
	t.Helper()
	ctx := context.Background()
	d, err := r.store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	spec, _, err := specFor(d)
	if err != nil {
		t.Fatal(err)
	}
	spec.Healthcheck = []string{"CMD-SHELL", "old-check"}
	spec.Labels[HashLabel] = hashSpec(spec)
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.store.SetAppliedSpec(ctx, id, string(raw)); err != nil {
		t.Fatal(err)
	}
	if c, ok := r.host.containers[ContainerName(id)]; ok {
		c.Hash = spec.Labels[HashLabel]
	}
	return spec
}

func TestNewDriverDefaultsDoNotRecreate(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	simulateOlderPanel(t, r, d.ID)
	r.host.reset()
	if err := r.svc.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m := r.host.mutating(); len(m) != 0 {
		t.Fatalf("a panel update touched the Database: %v", m)
	}
	v := r.view(t, d.ID)
	if v.Status != StatusRunning || !v.SettingsOutdated {
		t.Fatalf("status = %s, outdated = %v", v.Status, v.SettingsOutdated)
	}
}

func TestRecreateKeepsAppliedSpec(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	old := simulateOlderPanel(t, r, d.ID)
	delete(r.host.containers, ContainerName(d.ID))
	r.host.reset()
	if err := r.svc.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := r.host.specs[ContainerName(d.ID)]
	if r.host.count("create ") != 1 || !slices.Equal(got.Healthcheck, old.Healthcheck) || got.Labels[HashLabel] != old.Labels[HashLabel] {
		t.Fatalf("recreated from %+v; calls = %v", got, r.host.all())
	}
}

func TestApplySettingsRecreatesWithCurrent(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	simulateOlderPanel(t, r, d.ID)
	r.host.reset()
	if err := r.svc.ApplySettings(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	got := r.host.specs[ContainerName(d.ID)]
	if r.host.count("create ") != 1 || !slices.Equal(got.Healthcheck, Engines["postgres"].Healthcheck()) {
		t.Fatalf("spec = %+v; calls = %v", got, r.host.all())
	}
	if v := r.view(t, d.ID); v.SettingsOutdated || v.Status != StatusRunning {
		t.Fatalf("status = %s, outdated = %v", v.Status, v.SettingsOutdated)
	}
}

func TestApplySettingsWhileStoppedWaitsForStart(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	if err := r.svc.Stop(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	simulateOlderPanel(t, r, d.ID)
	r.host.reset()
	if err := r.svc.ApplySettings(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if r.host.count("create ") != 0 || r.host.count("start ") != 0 {
		t.Fatalf("a stopped Database was started: %v", r.host.all())
	}
	if err := r.svc.Start(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if r.host.count("create ") != 1 {
		t.Fatalf("start did not apply the settings: %v", r.host.all())
	}
}

func TestBackfillAppliedSpec(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	if err := r.store.SetAppliedSpec(ctx, d.ID, ""); err != nil {
		t.Fatal(err)
	}
	r.host.reset()
	if err := r.svc.ReconcileAll(ctx); err != nil {
		t.Fatal(err)
	}
	if m := r.host.mutating(); len(m) != 0 {
		t.Fatalf("backfill touched the container: %v", m)
	}
	if got, _ := r.store.Get(ctx, d.ID); got.AppliedSpec == "" {
		t.Fatal("applied spec not backfilled")
	}
}

func TestRecreateAfterRenameUsesNewAlias(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	simulateOlderPanel(t, r, d.ID)
	if err := r.svc.Rename(ctx, d.ID, "Blog"); err != nil {
		t.Fatal(err)
	}
	delete(r.host.containers, ContainerName(d.ID))
	if err := r.svc.ReconcileAll(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.host.specs[ContainerName(d.ID)].Alias; got != "blog.db.internal" {
		t.Fatalf("alias = %q", got)
	}
}

func TestUpdateImageAppliesSettings(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	simulateOlderPanel(t, r, d.ID)
	r.host.reset()
	if err := r.svc.UpdateImage(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	if r.host.count("create ") != 1 {
		t.Fatalf("calls = %v", r.host.all())
	}
	if v := r.view(t, d.ID); v.SettingsOutdated {
		t.Fatal("still outdated")
	}
}

func TestUpdateImageWhileStoppedDoesNotStart(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	if err := r.svc.Stop(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	r.host.images["postgres:18"] = "sha256:newer"
	r.host.reset()
	if err := r.svc.UpdateImage(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if r.host.count("create ") != 0 || r.host.count("start ") != 0 {
		t.Fatalf("a stopped Database was started: %v", r.host.all())
	}
	if v := r.view(t, d.ID); v.Status != StatusStopped {
		t.Fatalf("status = %s", v.Status)
	}
	if err := r.svc.Start(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if c := r.host.containers[ContainerName(d.ID)]; c == nil || c.ImageID != "sha256:newer" || !c.Running {
		t.Fatalf("container = %+v", c)
	}
}
