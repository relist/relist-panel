package databases

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"
)

type rig struct {
	svc    *Service
	host   *fakeHost
	store  *Store
	locked bool
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{host: newFakeHost(), store: newStore(t)}
	r.svc = New(Deps{Store: r.store, Host: r.host, Locked: func(context.Context) bool { return r.locked }})
	return r
}

func (r *rig) create(t *testing.T, name, engine, version string) Database {
	t.Helper()
	d, err := r.svc.Create(context.Background(), CreateInput{Name: name, Engine: engine, Version: version})
	if err != nil {
		t.Fatalf("Create %s: %v", name, err)
	}
	r.svc.Wait()
	return d
}

func (r *rig) view(t *testing.T, id int64) View {
	t.Helper()
	v, err := r.svc.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCreateRunsContainer(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	if !r.host.saw("ensure postgres:18") || !r.host.saw("create relist-db-1") {
		t.Fatalf("calls = %v", r.host.all())
	}
	spec := r.host.specs["relist-db-1"]
	if spec.Alias != "shop.db.internal" || spec.DataPath != "/var/lib/postgresql" || spec.Labels[IDLabel] != "1" ||
		spec.Volume != "relist-db-1" || spec.Labels[HashLabel] == "" {
		t.Fatalf("spec = %+v", spec)
	}
	got, _ := r.store.Get(context.Background(), d.ID)
	if !got.Initialized {
		t.Fatal("not Initialized")
	}
	if v := r.view(t, d.ID); v.Status != StatusRunning {
		t.Fatalf("status = %s", v.Status)
	}
}

func TestCreateRejectsUnknownVersion(t *testing.T) {
	r := newRig(t)
	_, err := r.svc.Create(context.Background(), CreateInput{Name: "A", Engine: "postgres", Version: "9"})
	if !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("err = %v", err)
	}
	if l, _ := r.store.List(context.Background()); len(l) != 0 {
		t.Fatalf("inserted %v", l)
	}
	if _, err := r.svc.Create(context.Background(), CreateInput{Name: " ", Engine: "redis", Version: "8"}); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("blank name err = %v", err)
	}
}

func TestCreateRejectsSlugConflict(t *testing.T) {
	r := newRig(t)
	r.create(t, "Shop", "redis", "8")
	_, err := r.svc.Create(context.Background(), CreateInput{Name: "shop!", Engine: "redis", Version: "8"})
	if !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateGeneratesPassword(t *testing.T) {
	r := newRig(t)
	a := r.create(t, "A", "redis", "8")
	b := r.create(t, "B", "redis", "8")
	if len(a.Password) != 43 || a.Password == b.Password {
		t.Fatalf("passwords %q %q", a.Password, b.Password)
	}
	if raw, err := base64.RawURLEncoding.DecodeString(a.Password); err != nil || len(raw) != 32 {
		t.Fatalf("not 32 bytes of base64url: %v", err)
	}
}

func TestAliasSkippedForThaiName(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "ฐานข้อมูล", "mysql", "8.4")
	if a := r.host.specs[ContainerName(d.ID)].Alias; a != "" {
		t.Fatalf("alias = %q", a)
	}
	v := r.view(t, d.ID)
	if len(v.Warnings) == 0 || !strings.Contains(strings.ToLower(v.Warnings[0]), "hostname") {
		t.Fatalf("warnings = %v", v.Warnings)
	}
	// Two Thai names both slug to "x" and must not clash.
	if _, err := r.svc.Create(context.Background(), CreateInput{Name: "อีกอัน", Engine: "mysql", Version: "8.4"}); err != nil {
		t.Fatalf("second Thai name: %v", err)
	}
	r.svc.Wait()
}

func TestReconcileRecreatesOnHashChange(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.host.containers["relist-db-1"].Hash = "stale"
	if v := r.view(t, d.ID); v.Status != StatusDrift {
		t.Fatalf("status = %s", v.Status)
	}
	r.host.reset()
	if err := r.svc.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.host.count("create relist-db-1") != 1 || r.host.count("remove-volume") != 0 {
		t.Fatalf("calls = %v", r.host.all())
	}
	if v := r.view(t, d.ID); v.Status != StatusRunning {
		t.Fatalf("status after = %s", v.Status)
	}
}

func TestReconcileStartsStopped(t *testing.T) {
	r := newRig(t)
	r.create(t, "Shop", "postgres", "18")
	r.host.containers["relist-db-1"].Running = false
	r.host.reset()
	if err := r.svc.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !r.host.saw("start relist-db-1") || r.host.count("create") != 0 {
		t.Fatalf("calls = %v", r.host.all())
	}
}

func TestReconcileLeavesDesiredStopped(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	if err := r.svc.Stop(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	r.host.reset()
	if err := r.svc.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m := r.host.mutating(); len(m) != 0 {
		t.Fatalf("calls = %v", m)
	}
	if v := r.view(t, d.ID); v.Status != StatusStopped {
		t.Fatalf("status = %s", v.Status)
	}
}

func TestReconcileVolumeMissingDoesNothing(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	delete(r.host.containers, "relist-db-1")
	delete(r.host.volumes, "relist-db-1")
	r.host.reset()
	if err := r.svc.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m := r.host.mutating(); len(m) != 0 {
		t.Fatalf("calls = %v", m)
	}
	if v := r.view(t, d.ID); v.Status != StatusVolumeMissing {
		t.Fatalf("status = %s", v.Status)
	}
}

func TestReconcileSkipsWhileLocked(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.host.containers["relist-db-1"].Running = false
	r.host.reset()
	r.locked = true
	if err := r.svc.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(r.host.all()) != 0 {
		t.Fatalf("calls = %v", r.host.all())
	}
	got, _ := r.store.Get(context.Background(), d.ID)
	if got.LastError != "" {
		t.Fatalf("LastError = %q", got.LastError)
	}
	if err := r.svc.Start(context.Background(), d.ID); !errors.Is(err, ErrLocked) {
		t.Fatalf("Start err = %v", err)
	}
	if got, _ := r.store.Get(context.Background(), d.ID); got.LastError != "" {
		t.Fatalf("Start stored LastError %q", got.LastError)
	}
}

func TestMigrationCallsIgnoreLock(t *testing.T) {
	r := newRig(t)
	r.create(t, "Shop", "postgres", "18")
	r.locked = true
	if err := r.svc.StopAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !r.host.saw("stop relist-db-1") {
		t.Fatalf("calls = %v", r.host.all())
	}
	delete(r.host.containers, "relist-db-1") // volume stays
	if err := r.svc.StartWanted(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.host.count("create relist-db-1") != 2 {
		t.Fatalf("calls = %v", r.host.all())
	}
}

func TestStartWantedSkipsStopped(t *testing.T) {
	r := newRig(t)
	a := r.create(t, "A", "postgres", "18")
	b := r.create(t, "B", "redis", "8")
	ctx := context.Background()
	_ = r.svc.Stop(ctx, a.ID)
	_ = r.svc.Stop(ctx, b.ID)
	if err := r.store.SetDesired(ctx, b.ID, DesiredRunning); err != nil {
		t.Fatal(err)
	}
	r.host.reset()
	if err := r.svc.StartWanted(ctx); err != nil {
		t.Fatal(err)
	}
	if !r.host.saw("start relist-db-2") || r.host.saw("start relist-db-1") {
		t.Fatalf("calls = %v", r.host.all())
	}
}

func TestRenameRealiasesWithoutRecreate(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.host.reset()
	if err := r.svc.Rename(context.Background(), d.ID, "Blog"); err != nil {
		t.Fatal(err)
	}
	if !r.host.saw("realias relist-db-1 blog.db.internal") || r.host.count("create") != 0 || r.host.count("restart") != 0 {
		t.Fatalf("calls = %v", r.host.all())
	}
	got, _ := r.store.Get(context.Background(), d.ID)
	if got.Name != "Blog" || got.Slug != "blog" {
		t.Fatalf("stored = %+v", got)
	}
	if v := r.view(t, d.ID); v.Status != StatusRunning {
		t.Fatalf("status after rename = %s (rename must not look like drift)", v.Status)
	}
}

func TestStopSetsDesired(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	ctx := context.Background()
	if err := r.svc.Stop(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := r.store.Get(ctx, d.ID)
	if got.Desired != DesiredStopped || !r.host.saw("stop relist-db-1") {
		t.Fatalf("desired=%s calls=%v", got.Desired, r.host.all())
	}
	r.host.reset()
	_ = r.svc.ReconcileAll(ctx)
	if r.host.count("start") != 0 {
		t.Fatal("ReconcileAll started a stopped Database")
	}
}

func TestRestartRestarts(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.host.reset()
	if err := r.svc.Restart(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	if !r.host.saw("restart relist-db-1") {
		t.Fatalf("calls = %v", r.host.all())
	}
}

func TestUpdateImageRecreatesOnlyWhenChanged(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.host.reset()
	if err := r.svc.UpdateImage(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	if !r.host.saw("pull postgres:18") || r.host.count("create") != 0 {
		t.Fatalf("same image: calls = %v", r.host.all())
	}
	r.host.images["postgres:18"] = "sha256:newer"
	r.host.reset()
	if err := r.svc.UpdateImage(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	if r.host.count("create relist-db-1") != 1 {
		t.Fatalf("new image: calls = %v", r.host.all())
	}
}

func TestDeleteNeedsConfirm(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	ctx := context.Background()
	r.host.reset()
	if err := r.svc.Delete(ctx, d.ID, "nope"); !errors.Is(err, ErrConfirm) {
		t.Fatalf("err = %v", err)
	}
	if len(r.host.all()) != 0 {
		t.Fatalf("calls = %v", r.host.all())
	}
	if err := r.svc.Delete(ctx, d.ID, "Shop"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.host.all(), []string{"remove relist-db-1", "remove-volume relist-db-1"}) {
		t.Fatalf("calls = %v", r.host.all())
	}
	if _, err := r.store.Get(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row still there: %v", err)
	}
}

func TestFailureStoresLastError(t *testing.T) {
	r := newRig(t)
	r.host.failOn["create "] = errors.New("docker: boom")
	d := r.create(t, "Shop", "postgres", "18")
	v := r.view(t, d.ID)
	if v.Status != StatusError || !strings.Contains(v.LastError, "boom") {
		t.Fatalf("status=%s lastError=%q", v.Status, v.LastError)
	}
	delete(r.host.failOn, "create ")
	if err := r.svc.Repair(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	v = r.view(t, d.ID)
	if v.Status != StatusRunning || v.LastError != "" {
		t.Fatalf("after repair status=%s lastError=%q", v.Status, v.LastError)
	}
}

func TestVolumesListsInitializedOnly(t *testing.T) {
	r := newRig(t)
	r.create(t, "A", "redis", "8")
	r.host.failOn["create "] = errors.New("docker: boom")
	r.create(t, "B", "redis", "8")
	got, err := r.svc.Volumes(context.Background())
	if err != nil || !slices.Equal(got, []string{"relist-db-1"}) {
		t.Fatalf("Volumes = %v, %v", got, err)
	}
}
