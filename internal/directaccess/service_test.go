package directaccess

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOpen(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	a := r.open(t)
	s := r.host.started[0]
	if s.BindIP != "127.0.0.1" || s.Upstream != "relist-db-7:5432" || s.ListenPort != 5432 || s.TargetLabel != "database:7" ||
		s.Network != "relist-network" || !s.Deadline.Equal(r.now.Add(60*time.Minute)) || s.HostPort != 0 || s.AccessID != a.ID {
		t.Fatalf("ProxySpec = %+v", s)
	}
	got, err := r.store.Get(ctx, a.ID)
	if err != nil || got.HostPort.HostPort != 49153 || got.HostPort.Bind != "127.0.0.1" || got.OpenedBy != 2 ||
		got.Target != (Target{KindDatabase, 7, 5432}) || got.ExpiresAt == nil || !got.ExpiresAt.Equal(r.now.Add(time.Hour)) {
		t.Fatalf("row = %+v, %v", got, err)
	}
	if a.HostPort.HostPort != 49153 {
		t.Fatalf("returned = %+v", a)
	}

	// All interfaces and a chosen port.
	r2 := newRig(t)
	if _, err := r2.svc.Open(ctx, OpenInput{TargetKind: KindDatabase, TargetID: 7, Channel: ChannelHostPort, AllInterfaces: true, HostPort: 15432, Minutes: 15}); err != nil {
		t.Fatal(err)
	}
	if s := r2.host.started[0]; s.BindIP != "0.0.0.0" || s.HostPort != 15432 || !s.Deadline.Equal(r2.now.Add(15*time.Minute)) {
		t.Fatalf("ProxySpec = %+v", s)
	}
}

func TestOpenRefuses(t *testing.T) {
	ctx := context.Background()
	base := OpenInput{TargetKind: KindDatabase, TargetID: 7, Channel: ChannelHostPort, Minutes: 60}
	with := func(f func(*OpenInput)) OpenInput { in := base; f(&in); return in }

	r := newRig(t)
	for name, tc := range map[string]struct {
		in   OpenInput
		want error
	}{
		"minutes 30":      {with(func(i *OpenInput) { i.Minutes = 30 }), ErrInvalid},
		"port 80":         {with(func(i *OpenInput) { i.HostPort = 80 }), ErrInvalid},
		"port too high":   {with(func(i *OpenInput) { i.HostPort = 65536 }), ErrInvalid},
		"kind service":    {with(func(i *OpenInput) { i.TargetKind = "service" }), ErrUnsupported},
		"channel tailnet": {with(func(i *OpenInput) { i.Channel = "tailscale" }), ErrUnsupported},
		"missing target":  {with(func(i *OpenInput) { i.TargetID = 99 }), ErrTargetNotFound},
	} {
		if _, err := r.svc.Open(ctx, tc.in); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	if len(r.host.started) != 0 {
		t.Fatalf("a refused open started a proxy: %+v", r.host.started)
	}

	r.open(t)
	if _, err := r.svc.Open(ctx, base); !errors.Is(err, ErrOpen) {
		t.Fatalf("second open: %v, want ErrOpen", err)
	}

	r2 := newRig(t)
	r2.targets.eps["database:7"] = Endpoint{Name: "Shop", Network: "relist-network", Host: "relist-db-7", Port: 5432, Running: false}
	if _, err := r2.svc.Open(ctx, base); !errors.Is(err, ErrTargetStopped) {
		t.Fatalf("stopped: %v", err)
	}
	r3 := newRig(t)
	r3.locked = true
	if _, err := r3.svc.Open(ctx, base); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked: %v", err)
	}
}

func TestOpenReplacesAnExpiredRow(t *testing.T) {
	r := newRig(t)
	r.open(t)
	r.now = r.now.Add(2 * time.Hour)
	if _, err := r.svc.Open(context.Background(), OpenInput{TargetKind: KindDatabase, TargetID: 7, Channel: ChannelHostPort, Minutes: 15}); err != nil {
		t.Fatalf("open after expiry: %v", err)
	}
}

func TestOpenFailureLeavesNothing(t *testing.T) {
	r := newRig(t)
	r.host.startErr = ErrPortInUse
	_, err := r.svc.Open(context.Background(), OpenInput{TargetKind: KindDatabase, TargetID: 7, Channel: ChannelHostPort, HostPort: 15432, Minutes: 60})
	if !errors.Is(err, ErrPortInUse) {
		t.Fatalf("err = %v, want ErrPortInUse", err)
	}
	rows, _ := r.store.List(context.Background())
	if len(rows) != 0 {
		t.Fatalf("rows left behind: %+v", rows)
	}
	if !r.host.saw("remove relist-access-1") {
		t.Fatalf("RemoveProxy not called for the inserted ID: %v", r.host.calls)
	}
}

func TestExtend(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	a := r.open(t)
	r.now = r.now.Add(10 * time.Minute)
	got, err := r.svc.Extend(ctx, a.ID, 240)
	if err != nil {
		t.Fatal(err)
	}
	want := r.now.Add(240 * time.Minute)
	if !r.host.extended[a.ID].Equal(want) || got.ExpiresAt == nil || !got.ExpiresAt.Equal(want) {
		t.Fatalf("extended to %v / %+v, want %v", r.host.extended[a.ID], got.ExpiresAt, want)
	}
	row, _ := r.store.Get(ctx, a.ID)
	if !row.ExpiresAt.Equal(want) {
		t.Fatalf("row expiry %v", row.ExpiresAt)
	}
	if _, err := r.svc.Extend(ctx, a.ID, 30); !errors.Is(err, ErrInvalid) {
		t.Fatalf("minutes 30: %v", err)
	}
}

func TestExtendAfterExpiry(t *testing.T) {
	ctx := context.Background()
	// The row is already past its expiry: no exec at all.
	r := newRig(t)
	a := r.open(t)
	r.now = r.now.Add(2 * time.Hour)
	if _, err := r.svc.Extend(ctx, a.ID, 60); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if r.host.saw("extend relist-access-1") {
		t.Fatal("Host.Extend was called for an expired row")
	}
	if _, err := r.store.Get(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row not deleted: %v", err)
	}

	// The proxy exited between the page load and the click.
	r2 := newRig(t)
	a2 := r2.open(t)
	r2.host.extendEr = ErrGone
	if _, err := r2.svc.Extend(ctx, a2.ID, 60); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := r2.store.Get(ctx, a2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row not deleted: %v", err)
	}
}

func TestClose(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	a := r.open(t)
	if err := r.svc.Close(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if !r.host.saw("remove relist-access-1") {
		t.Fatalf("calls = %v", r.host.calls)
	}
	if _, err := r.store.Get(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row: %v", err)
	}
	if err := r.svc.Close(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestForTargetHidesExpired(t *testing.T) {
	r := newRig(t)
	r.open(t)
	got, _ := r.svc.ForTarget(context.Background(), KindDatabase, 7)
	if len(got) != 1 {
		t.Fatalf("ForTarget = %d", len(got))
	}
	r.now = r.now.Add(2 * time.Hour)
	got, _ = r.svc.ForTarget(context.Background(), KindDatabase, 7)
	if len(got) != 0 {
		t.Fatalf("expired row shown: %+v", got)
	}
}

func TestReconcileKeepsExtendedAccess(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	a := r.open(t)
	// Extend to 4 h, then the panel restarts 90 min later: the container's
	// expires label still says 60 min, but the table says 4 h.
	if _, err := r.svc.Extend(ctx, a.ID, 240); err != nil {
		t.Fatal(err)
	}
	r.now = r.now.Add(90 * time.Minute)
	restarted := New(Deps{Store: r.store, Host: r.host, Targets: r.targets, Now: func() time.Time { return r.now }})
	if err := restarted.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if r.host.saw("remove relist-access-1") {
		t.Fatalf("reconcile removed an extended access: %v", r.host.calls)
	}
	if _, err := r.store.Get(ctx, a.ID); err != nil {
		t.Fatalf("row deleted: %v", err)
	}
	if !r.host.proxies[a.ID] {
		t.Fatal("proxy gone")
	}
}

func TestReconcileRemoves(t *testing.T) {
	ctx := context.Background()
	exp := func(r *rig, d time.Duration) *time.Time { t := r.now.Add(d); return &t }

	// A container with no row.
	r := newRig(t)
	r.host.proxies[42] = true
	if err := r.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if r.host.proxies[42] {
		t.Fatal("orphan container kept")
	}

	// A row past its expiry: container removed and row deleted.
	r = newRig(t)
	id, _ := r.store.Insert(ctx, Access{Target: Target{KindDatabase, 7, 5432}, Channel: ChannelHostPort, HostPort: HostPort{"127.0.0.1", 1}, ExpiresAt: exp(r, -time.Minute), OpenedAt: r.now})
	r.host.proxies[id] = true
	if err := r.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.Get(ctx, id); !errors.Is(err, ErrNotFound) || r.host.proxies[id] {
		t.Fatalf("expired kept: %v %v", err, r.host.proxies)
	}

	// A row with no running container.
	r = newRig(t)
	id, _ = r.store.Insert(ctx, Access{Target: Target{KindDatabase, 7, 5432}, Channel: ChannelHostPort, HostPort: HostPort{"127.0.0.1", 1}, ExpiresAt: exp(r, time.Hour), OpenedAt: r.now})
	if err := r.svc.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.Get(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row without container kept: %v", err)
	}
}

func TestCloseTarget(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	a := r.open(t)
	other, _ := r.store.Insert(ctx, Access{Target: Target{KindDatabase, 8, 5432}, Channel: ChannelHostPort, HostPort: HostPort{"127.0.0.1", 1}, OpenedAt: r.now})
	if err := r.svc.CloseTarget(ctx, KindDatabase, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.Get(ctx, a.ID); !errors.Is(err, ErrNotFound) || !r.host.saw("remove relist-access-1") {
		t.Fatalf("not closed: %v %v", err, r.host.calls)
	}
	if _, err := r.store.Get(ctx, other); err != nil {
		t.Fatalf("another target's access closed: %v", err)
	}
}
