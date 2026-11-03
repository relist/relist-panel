package builders

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

const newRef = "moby/buildkit:v0.35.0@sha256:" + hex64

func TestAcquireEnsuresEveryTime(t *testing.T) {
	f := newServiceFixture(t)
	b := f.builder(t, "large")
	for i := 0; i < 2; i++ {
		var out bytes.Buffer
		name, release, err := f.svc.Acquire(context.Background(), b.ID, &out)
		if err != nil {
			t.Fatal(err)
		}
		release()
		if name != "relist-builder-1" {
			t.Fatalf("name = %q", name)
		}
		if got := out.String(); got != "Builder: large (2 CPU, 4 GB, 4 parallel steps)\n" {
			t.Fatalf("log = %q", got)
		}
	}
	if n := f.engine.count("Ensure"); n != 2 {
		t.Fatalf("Ensure calls = %d (%v)", n, f.engine.Calls())
	}
	if got := f.engine.lastSpec(); got != SpecFor(b, DefaultBuildKitImage) {
		t.Fatalf("spec = %+v", got)
	}
}

func TestAcquireDockerDefault(t *testing.T) {
	f := newServiceFixture(t)
	var out bytes.Buffer
	name, release, err := f.svc.Acquire(context.Background(), DockerDefault, &out)
	if err != nil || name != "" {
		t.Fatalf("Acquire = %q, %v", name, err)
	}
	release()
	if out.String() != "Builder: Docker default (no limits)\n" || len(f.engine.Calls()) != 0 {
		t.Fatalf("log = %q, calls = %v", out.String(), f.engine.Calls())
	}
}

func TestAcquireSwitchesImageWhenIdle(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	b := f.builder(t, "large")
	if err := f.store.SetBuildKitImage(ctx, newRef); err != nil {
		t.Fatal(err)
	}
	f.engine.inspection = Inspection{State: StateRunning, Image: DefaultBuildKitImage}
	var out bytes.Buffer
	_, release, err := f.svc.Acquire(ctx, b.ID, &out)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if f.engine.count("Recreate") != 1 || f.engine.lastSpec().Image != newRef {
		t.Fatalf("calls = %v, spec = %+v", f.engine.Calls(), f.engine.lastSpec())
	}
	if !strings.HasPrefix(out.String(), "Builder large: switching BuildKit image to "+newRef+"\n") {
		t.Fatalf("log = %q", out.String())
	}
}

func TestAcquireDefersImageSwitchWhenBusy(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	b := f.builder(t, "large")
	f.engine.inspection = Inspection{State: StateRunning, Image: DefaultBuildKitImage}
	_, first, err := f.svc.Acquire(ctx, b.ID, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	if err := f.store.SetBuildKitImage(ctx, newRef); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_, second, err := f.svc.Acquire(ctx, b.ID, &out)
	if err != nil {
		t.Fatal(err)
	}
	second()
	if f.engine.count("Recreate") != 0 || f.engine.lastSpec().Image != DefaultBuildKitImage {
		t.Fatalf("calls = %v, spec = %+v", f.engine.Calls(), f.engine.lastSpec())
	}
	want := "Builder large still runs " + DefaultBuildKitImage + "; it switches to " + newRef + " once its other builds finish\n"
	if !strings.HasPrefix(out.String(), want) {
		t.Fatalf("log = %q, want prefix %q", out.String(), want)
	}
}

func TestAcquireMissingContainerCreatesWithSetting(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	b := f.builder(t, "large")
	if err := f.store.SetBuildKitImage(ctx, newRef); err != nil {
		t.Fatal(err)
	}
	_, release, err := f.svc.Acquire(ctx, b.ID, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	release()
	if f.engine.count("Ensure") != 1 || f.engine.count("Recreate") != 0 || f.engine.lastSpec().Image != newRef {
		t.Fatalf("calls = %v, spec = %+v", f.engine.Calls(), f.engine.lastSpec())
	}
}

func TestAcquireWaitsForARecreate(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	b := f.builder(t, "large")
	f.engine.inspection = Inspection{State: StateRunning, Image: DefaultBuildKitImage}
	f.engine.recreateHit = make(chan struct{})
	edited := b
	edited.MaxParallelism = 2
	done := make(chan error, 1)
	go func() { _, err := f.svc.Update(ctx, edited); done <- err }()
	for f.engine.count("Recreate") == 0 {
		time.Sleep(time.Millisecond)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, _, err := f.svc.Acquire(waitCtx, b.ID, &bytes.Buffer{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire during a re-create = %v, want it to wait", err)
	}
	close(f.engine.recreateHit)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, release, err := f.svc.Acquire(ctx, b.ID, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
}

func TestLogLineMemoryUnits(t *testing.T) {
	for _, tc := range []struct {
		b    Builder
		want string
	}{
		{Builder{Name: "a", CPUsMilli: 500, MemoryMB: 1536}, "a (0.5 CPU, 1536 MB)"},
		{Builder{Name: "b", CPUsMilli: 2000, MemoryMB: 2048, MaxParallelism: 3}, "b (2 CPU, 2 GB, 3 parallel steps)"},
		{Builder{Name: "c", CPUsMilli: 1500, MemoryMB: 256}, "c (1.5 CPU, 256 MB)"},
	} {
		if got := Describe(tc.b); got != tc.want {
			t.Errorf("Describe(%+v) = %q, want %q", tc.b, got, tc.want)
		}
	}
}

func TestAcquireFailureRecordsLastError(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	b := f.builder(t, "large")
	f.engine.ensureErr = errors.New("boom")
	_, _, err := f.svc.Acquire(ctx, b.ID, &bytes.Buffer{})
	if err == nil || err.Error() != "Could not start Builder large: boom" {
		t.Fatalf("err = %v", err)
	}
	var start *StartError
	if !errors.As(err, &start) {
		t.Fatalf("err %T is not a *StartError", err)
	}
	if got, _ := f.store.Get(ctx, b.ID); got.LastError != "boom" {
		t.Fatalf("last_error = %q", got.LastError)
	}
	if f.svc.busy(b.ID) != 0 {
		t.Fatalf("busy = %d after a failed Acquire", f.svc.busy(b.ID))
	}
	f.engine.ensureErr = nil
	_, release, err := f.svc.Acquire(ctx, b.ID, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	release()
	if got, _ := f.store.Get(ctx, b.ID); got.LastError != "" {
		t.Fatalf("last_error = %q after a good Acquire", got.LastError)
	}
}

func TestUpdateKeepsRowWhenDockerRefuses(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	b := f.builder(t, "large")
	f.engine.inspection = Inspection{State: StateRunning, Image: DefaultBuildKitImage}
	f.engine.ensureErr = errors.New("Cannot update container: memory is below usage")
	edited := b
	edited.MemoryMB = 1024
	if _, err := f.svc.Update(ctx, edited); err == nil || !strings.Contains(err.Error(), "below usage") {
		t.Fatalf("Update err = %v", err)
	}
	if got, _ := f.store.Get(ctx, b.ID); got.MemoryMB != 4096 {
		t.Fatalf("memory = %d, want the row unchanged", got.MemoryMB)
	}
	if spec := f.engine.lastSpec(); spec.MemoryBytes != 1024<<20 || spec.Image != DefaultBuildKitImage {
		t.Fatalf("spec = %+v, want the new limit on the container's own image", spec)
	}
}

func TestUpdateOfUncreatedBuilderOnlySaves(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	b := f.builder(t, "large")
	edited := b
	edited.MemoryMB, edited.MaxParallelism = 1024, 2
	got, err := f.svc.Update(ctx, edited)
	if err != nil || got.MemoryMB != 1024 || got.MaxParallelism != 2 {
		t.Fatalf("Update = %+v, %v", got, err)
	}
	if f.engine.count("Ensure")+f.engine.count("Recreate") != 0 {
		t.Fatalf("calls = %v", f.engine.Calls())
	}
}

func TestParallelismEditRefusedWhileBusy(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	b := f.builder(t, "large")
	f.engine.inspection = Inspection{State: StateRunning, Image: DefaultBuildKitImage}
	_, release, err := f.svc.Acquire(ctx, b.ID, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	edited := b
	edited.MaxParallelism = 2
	if _, err := f.svc.Update(ctx, edited); !errors.Is(err, ErrBusy) {
		t.Fatalf("Update while busy = %v, want ErrBusy", err)
	}
	release()
	if _, err := f.svc.Update(ctx, edited); err != nil {
		t.Fatal(err)
	}
	if f.engine.count("Recreate") != 1 || f.engine.lastSpec().MaxParallelism != 2 {
		t.Fatalf("calls = %v", f.engine.Calls())
	}
}

func TestCPUMemoryEditAllowedWhileBusy(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	b := f.builder(t, "large")
	f.engine.inspection = Inspection{State: StateRunning, Image: DefaultBuildKitImage}
	_, release, err := f.svc.Acquire(ctx, b.ID, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	edited := b
	edited.CPUsMilli = 1000
	if _, err := f.svc.Update(ctx, edited); err != nil {
		t.Fatal(err)
	}
	if f.engine.count("Recreate") != 0 || f.engine.lastSpec().CPUQuota != 100000 {
		t.Fatalf("calls = %v, spec = %+v", f.engine.Calls(), f.engine.lastSpec())
	}
}

func TestDeleteGuards(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	b := f.builder(t, "large")
	c := f.compose(t, "web")
	if err := f.store.SetDefault(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetComposeChoice(ctx, c, Choice{Mode: ModeBuilder, BuilderID: b.ID}); err != nil {
		t.Fatal(err)
	}
	f.deps.uses[b.ID] = true
	if err := f.svc.Delete(ctx, b.ID); !errors.Is(err, ErrIsDefault) {
		t.Fatalf("Delete default = %v", err)
	}
	_ = f.store.SetDefault(ctx, DockerDefault)
	var inUse *InUseError
	if err := f.svc.Delete(ctx, b.ID); !errors.As(err, &inUse) || !reflect.DeepEqual(inUse.Composes, []string{"web"}) {
		t.Fatalf("Delete used = %v", err)
	}
	if inUse.Error() != "These Composes use this Builder: web" {
		t.Fatalf("message = %q", inUse.Error())
	}
	_ = f.store.SetComposeChoice(ctx, c, Choice{Mode: ModeDefault})
	if err := f.svc.Delete(ctx, b.ID); !errors.Is(err, ErrQueued) {
		t.Fatalf("Delete queued = %v", err)
	}
	f.deps.uses[b.ID] = false
	if err := f.svc.Delete(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.engine.Calls(); got[len(got)-1] != "Remove relist-builder-1" {
		t.Fatalf("calls = %v", got)
	}
	if _, err := f.store.Get(ctx, b.ID); err == nil {
		t.Fatal("row still there")
	}
}

func TestPruneRefusedWhileBusy(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	b := f.builder(t, "large")
	_, release, err := f.svc.Acquire(ctx, b.ID, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Prune(ctx, b.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("Prune while busy = %v", err)
	}
	release()
	if err := f.svc.Prune(ctx, b.ID); err != nil || f.engine.count("Prune") != 1 {
		t.Fatalf("Prune = %v, calls %v", err, f.engine.Calls())
	}
}

func TestSetBuildKitImage(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	if err := f.svc.SetBuildKitImage(ctx, "moby/buildkit:latest"); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("invalid = %v", err)
	}
	f.engine.pullErr = errors.New("manifest unknown")
	var pull *PullError
	if err := f.svc.SetBuildKitImage(ctx, newRef); !errors.As(err, &pull) || err.Error() != "Could not pull "+newRef+": manifest unknown" {
		t.Fatalf("failed pull = %v", err)
	}
	if got, _ := f.store.BuildKitImage(ctx); got != "" {
		t.Fatalf("setting = %q after a failed pull", got)
	}
	f.engine.pullErr = nil
	if err := f.svc.SetBuildKitImage(ctx, newRef); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.store.BuildKitImage(ctx); got != newRef {
		t.Fatalf("setting = %q", got)
	}
	pulls := f.engine.count("Pull")
	if err := f.svc.SetBuildKitImage(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.store.BuildKitImage(ctx); got != "" || f.engine.count("Pull") != pulls {
		t.Fatalf("setting = %q, pulls %d → %d", got, pulls, f.engine.count("Pull"))
	}
	if f.engine.count("Pull") != 2 {
		t.Fatalf("pulls = %v", f.engine.Calls())
	}
}

func TestValidate(t *testing.T) {
	ctx := context.Background()
	f := newServiceFixture(t)
	f.builder(t, "large")
	ok := Builder{Name: "small", CPUsMilli: 1000, MemoryMB: 1024}
	for _, tc := range []struct {
		edit  func(*Builder)
		field string
	}{
		{func(b *Builder) { b.Name = "  " }, "name"},
		{func(b *Builder) { b.Name = strings.Repeat("x", 41) }, "name"},
		{func(b *Builder) { b.Name = "docker DEFAULT" }, "name"},
		{func(b *Builder) { b.Name = "LARGE" }, "name"},
		{func(b *Builder) { b.CPUsMilli = 50 }, "cpus"},
		{func(b *Builder) { b.CPUsMilli = 1550 }, "cpus"},
		{func(b *Builder) { b.CPUsMilli = 16100 }, "cpus"},
		{func(b *Builder) { b.MemoryMB = 255 }, "memoryMb"},
		{func(b *Builder) { b.MemoryMB = 31901 }, "memoryMb"},
		{func(b *Builder) { b.MaxParallelism = -1 }, "maxParallelism"},
		{func(b *Builder) { b.MaxParallelism = 65 }, "maxParallelism"},
	} {
		b := ok
		tc.edit(&b)
		_, err := f.svc.Create(ctx, b)
		var v *ValidationError
		if !errors.As(err, &v) || v.Field != tc.field {
			t.Errorf("Create(%+v) = %v, want a %s ValidationError", b, err, tc.field)
		}
	}
	b := ok
	b.Name = "  small  "
	got, err := f.svc.Create(ctx, b)
	if err != nil || got.Name != "small" {
		t.Fatalf("Create = %+v, %v", got, err)
	}
	got.CPUsMilli = 16000
	if _, err := f.svc.Update(ctx, got); err != nil {
		t.Fatalf("Update keeping its own name = %v", err)
	}
}
