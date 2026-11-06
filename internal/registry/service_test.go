package registry

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

type svcFixture struct {
	svc   *Service
	host  *fakeHost
	store *Store
	files Files
	using []string
}

func newService(t *testing.T) *svcFixture {
	t.Helper()
	f := &svcFixture{store: newStore(t), host: &fakeHost{t: t}, files: Files{Dir: filepath.Join(t.TempDir(), ".registry")}}
	f.svc = New(Deps{
		Store: f.store, Host: f.host, Files: f.files, PanelDomain: "panel.example.com",
		DomainHosts: func(context.Context) (map[string]string, error) {
			return map[string]string{"shop.example.com": "Acme"}, nil
		},
		ComposesUsing: func(_ context.Context, host string) ([]string, error) {
			if host == "registry.example.com" {
				return f.using, nil
			}
			return nil, nil
		},
	})
	return f
}

func (f *svcFixture) enable(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if err := f.svc.Save(ctx, Settings{Hostname: "registry.example.com", TLS: TLSLetsEncrypt}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Enable(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestHostnameConflicts(t *testing.T) {
	f := newService(t)
	ctx := context.Background()
	cases := map[string]string{
		"panel.example.com": "panel.example.com is the panel's own domain",
		"Shop.Example.com":  "shop.example.com is a Domain of Acme",
	}
	for host, want := range cases {
		var conflict *ErrHostnameConflict
		if err := f.svc.Save(ctx, Settings{Hostname: host, TLS: TLSLetsEncrypt}); !errors.As(err, &conflict) || conflict.Message != want {
			t.Errorf("%s: err = %v, want %q", host, err, want)
		}
	}
	for _, bad := range []string{"localhost", "reg.example.com:5000", "https://reg.example.com", "a b.com", ""} {
		if err := f.svc.Save(ctx, Settings{Hostname: bad, TLS: TLSLetsEncrypt}); !errors.Is(err, ErrInvalidHostname) {
			t.Errorf("%q: err = %v, want ErrInvalidHostname", bad, err)
		}
	}
	if err := f.svc.Save(ctx, Settings{Hostname: "reg.example.com", TLS: "plain"}); !errors.Is(err, ErrInvalidTLS) {
		t.Errorf("tls plain: err = %v", err)
	}
	if err := f.svc.Save(ctx, Settings{Hostname: " Registry.Example.com ", TLS: TLSCertificate}); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.store.Settings(ctx); st.Hostname != "registry.example.com" || st.TLS != TLSCertificate || st.Enabled {
		t.Fatalf("saved %+v", st)
	}
}

func TestEnableNeedsHostname(t *testing.T) {
	f := newService(t)
	if err := f.svc.Enable(context.Background()); !errors.Is(err, ErrNoHostname) {
		t.Fatalf("err = %v, want ErrNoHostname", err)
	}
}

func TestEnablePullsWritesCreates(t *testing.T) {
	f := newService(t)
	f.enable(t)
	if want := []string{"EnsureImage " + DefaultImage, "Create"}; !slices.Equal(f.host.calls[:2], want) && !slices.Equal(withoutInspect(f.host.calls), want) {
		t.Fatalf("calls = %v, want %v", f.host.calls, want)
	}
	if !f.host.filesAtCreate {
		t.Fatal("Create ran before the files were written")
	}
	st, _ := f.store.Settings(context.Background())
	if !st.Enabled || st.SystemSecret == "" || bcrypt.CompareHashAndPassword([]byte(st.SystemHash), []byte(st.SystemSecret)) != nil {
		t.Fatalf("settings = %+v", st)
	}
	spec := f.host.created[0]
	if spec.Hostname != "registry.example.com" || spec.TLS != TLSLetsEncrypt || spec.Image != DefaultImage || spec.Hash == "" || spec.FilesDir != f.files.Dir {
		t.Fatalf("spec = %+v", spec)
	}
	if status, _ := f.svc.Status(context.Background()); status != StatusRunning {
		t.Fatalf("status = %s", status)
	}
	secret := st.SystemSecret
	f.host.calls = nil
	if err := f.svc.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.store.Settings(context.Background()); st.SystemSecret != secret {
		t.Fatal("a second Enable replaced the system secret")
	}
}

func withoutInspect(calls []string) []string {
	return slices.DeleteFunc(slices.Clone(calls), func(c string) bool { return c == "Inspect" })
}

func TestEnablePullFailureSetsLastError(t *testing.T) {
	f := newService(t)
	f.host.pullErr = errors.New("toomanyrequests: rate limit")
	ctx := context.Background()
	if err := f.svc.Save(ctx, Settings{Hostname: "registry.example.com", TLS: TLSLetsEncrypt}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Enable(ctx); err == nil {
		t.Fatal("Enable succeeded with a failing pull")
	}
	st, _ := f.store.Settings(ctx)
	if st.Enabled || !strings.Contains(st.LastError, "rate limit") || slices.Contains(f.host.calls, "Create") {
		t.Fatalf("settings = %+v, calls = %v", st, f.host.calls)
	}
	if status, _ := f.svc.Status(ctx); status != StatusError {
		t.Fatalf("status = %s, want error", status)
	}
	f.host.pullErr = nil
	if err := f.svc.Enable(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.store.Settings(ctx); st.LastError != "" {
		t.Fatalf("last error kept after a good Enable: %q", st.LastError)
	}
}

func TestReconcileRecreatesOnHashChange(t *testing.T) {
	f := newService(t)
	f.enable(t)
	ctx := context.Background()
	f.host.state.Hash = "edited by hand"
	if status, _ := f.svc.Status(ctx); status != StatusDrift {
		t.Fatalf("status = %s, want drift", status)
	}
	f.host.calls = nil
	drift, err := f.svc.Reconcile(ctx)
	if err != nil || !drift || !slices.Contains(f.host.calls, "Create") {
		t.Fatalf("drift = %v, err = %v, calls = %v", drift, err, f.host.calls)
	}
	f.host.calls = nil
	if drift, _ := f.svc.Reconcile(ctx); drift || slices.Contains(f.host.calls, "Create") {
		t.Fatalf("second reconcile: drift = %v, calls = %v", drift, f.host.calls)
	}
}

func TestSaveWhileEnabledRecreates(t *testing.T) {
	f := newService(t)
	f.enable(t)
	f.host.calls = nil
	if err := f.svc.Save(context.Background(), Settings{Hostname: "cr.example.com", TLS: TLSCertificate}); err != nil {
		t.Fatal(err)
	}
	if last := f.host.created[len(f.host.created)-1]; last.Hostname != "cr.example.com" || last.TLS != TLSCertificate {
		t.Fatalf("recreated with %+v", last)
	}
}

func TestReconcileStartsMissingContainer(t *testing.T) {
	f := newService(t)
	f.enable(t)
	ctx := context.Background()
	f.host.state = ContainerState{}
	f.host.calls = nil
	if drift, err := f.svc.Reconcile(ctx); err != nil || !drift || !slices.Contains(f.host.calls, "Create") {
		t.Fatalf("missing: drift = %v, err = %v, calls = %v", drift, err, f.host.calls)
	}
	f.host.state.Running = false
	if status, _ := f.svc.Status(ctx); status != StatusStopped {
		t.Fatalf("status = %s, want stopped", status)
	}
	f.host.calls = nil
	if drift, err := f.svc.Reconcile(ctx); err != nil || !drift || !slices.Contains(f.host.calls, "Restart") {
		t.Fatalf("stopped: drift = %v, err = %v, calls = %v", drift, err, f.host.calls)
	}
}

func TestReconcileLeavesDisabledAlone(t *testing.T) {
	f := newService(t)
	f.host.state = ContainerState{Exists: true, Running: true, Hash: "someone else's"}
	drift, err := f.svc.Reconcile(context.Background())
	if err != nil || drift || len(withoutInspect(f.host.calls)) != 0 {
		t.Fatalf("drift = %v, err = %v, calls = %v", drift, err, f.host.calls)
	}
	if status, _ := f.svc.Status(context.Background()); status != StatusOff {
		t.Fatalf("status = %s, want off", status)
	}
}

func TestReconcileKeepsReadOnlyFlag(t *testing.T) {
	f := newService(t)
	f.enable(t)
	if err := f.files.Write(RenderConfig(true), []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ro, _ := f.files.ReadOnly(); !ro {
		t.Fatal("reconcile turned read-only off")
	}
	if status, _ := f.svc.Status(context.Background()); status != StatusReadOnly {
		t.Fatalf("status = %s, want read-only", status)
	}
}

func TestDisableWarnsComposesUsing(t *testing.T) {
	f := newService(t)
	f.enable(t)
	f.using = []string{"shop", "blog"}
	warnings, err := f.svc.Disable(context.Background())
	if err != nil || !slices.Equal(warnings, []string{"shop", "blog"}) || f.host.state.Exists {
		t.Fatalf("warnings = %v, err = %v, state = %+v", warnings, err, f.host.state)
	}
	if st, _ := f.store.Settings(context.Background()); st.Enabled || st.Hostname == "" {
		t.Fatalf("settings = %+v", st)
	}
	if slices.Contains(f.host.calls, "RemoveVolume") {
		t.Fatal("Disable removed the volume")
	}
}

func TestDeleteDataRequiresDisabledAndHostname(t *testing.T) {
	f := newService(t)
	f.enable(t)
	ctx := context.Background()
	if err := f.svc.DeleteData(ctx, "registry.example.com"); !errors.Is(err, ErrStillEnabled) {
		t.Fatalf("enabled: err = %v", err)
	}
	if _, err := f.svc.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.DeleteData(ctx, "wrong"); !errors.Is(err, ErrConfirm) {
		t.Fatalf("wrong confirm: err = %v", err)
	}
	if err := f.svc.DeleteData(ctx, "registry.example.com"); err != nil || !slices.Contains(f.host.calls, "RemoveVolume") {
		t.Fatalf("err = %v, calls = %v", err, f.host.calls)
	}
}

func TestHostnameOnlyWhenEnabled(t *testing.T) {
	f := newService(t)
	ctx := context.Background()
	_ = f.svc.Save(ctx, Settings{Hostname: "registry.example.com", TLS: TLSLetsEncrypt})
	if _, ok := f.svc.Hostname(ctx); ok {
		t.Fatal("Hostname reported before Enable")
	}
	_ = f.svc.Enable(ctx)
	if h, ok := f.svc.Hostname(ctx); !ok || h != "registry.example.com" {
		t.Fatalf("Hostname = %q, %v", h, ok)
	}
}

func TestReconcileClearsAStaleError(t *testing.T) {
	f := newService(t)
	f.enable(t)
	ctx := context.Background()
	if err := f.store.SetLastError(ctx, "toomanyrequests: rate limit"); err != nil {
		t.Fatal(err)
	}
	if status, _ := f.svc.Status(ctx); status != StatusError {
		t.Fatalf("status = %s, want error", status)
	}
	if _, err := f.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if status, _ := f.svc.Status(ctx); status != StatusRunning {
		t.Fatalf("status after repair = %s, want running", status)
	}
}
