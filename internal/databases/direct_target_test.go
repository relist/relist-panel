package databases

import (
	"context"
	"errors"
	"slices"
	"testing"

	"relist-panel/internal/directaccess"
)

func TestDirectTargetResolve(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	targets := r.svc.DirectTarget()

	ep, err := targets.Resolve(ctx, directaccess.KindDatabase, d.ID)
	want := directaccess.Endpoint{Name: "Shop", Network: Network, Host: "relist-db-1", Port: 5432, Running: true}
	if err != nil || ep != want {
		t.Fatalf("running: %+v, %v; want %+v", ep, err, want)
	}

	if err := r.svc.Stop(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	ep, err = targets.Resolve(ctx, directaccess.KindDatabase, d.ID)
	if err != nil || ep.Running {
		t.Fatalf("stopped: %+v, %v", ep, err)
	}

	if _, err := targets.Resolve(ctx, directaccess.KindDatabase, 99); !errors.Is(err, directaccess.ErrTargetNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := targets.Resolve(ctx, "service", d.ID); !errors.Is(err, directaccess.ErrTargetNotFound) {
		t.Fatalf("other kind: %v", err)
	}

	// A tombstone: deleted with its Backups kept.
	r2 := newRig(t)
	d2 := r2.create(t, "Gone", "postgres", "18")
	if _, err := r2.store.InsertBackup(ctx, Backup{DatabaseID: d2.ID, LogicalName: "app"}); err != nil {
		t.Fatal(err)
	}
	if err := r2.svc.Delete(ctx, d2.ID, "Gone", false); err != nil {
		t.Fatal(err)
	}
	if _, err := r2.svc.DirectTarget().Resolve(ctx, directaccess.KindDatabase, d2.ID); !errors.Is(err, directaccess.ErrTargetNotFound) {
		t.Fatalf("tombstone: %v", err)
	}
}

type fakeCloser struct {
	host  *fakeHost
	err   error
	calls []string
}

func (c *fakeCloser) CloseTarget(_ context.Context, kind string, id int64) error {
	c.host.mu.Lock()
	defer c.host.mu.Unlock()
	c.host.calls = append(c.host.calls, "close-access "+kind)
	c.calls = append(c.calls, kind)
	return c.err
}

func TestDeleteDatabaseClosesAccess(t *testing.T) {
	r := newRig(t)
	closer := &fakeCloser{host: r.host}
	r.svc.d.DirectAccess = closer
	d := r.create(t, "Shop", "postgres", "18")
	ctx := context.Background()
	r.host.reset()
	if err := r.svc.Delete(ctx, d.ID, "Shop", false); err != nil {
		t.Fatal(err)
	}
	if got := r.host.all(); !slices.Equal(got, []string{"close-access database", "remove relist-db-1", "remove-volume relist-db-1"}) {
		t.Fatalf("calls = %v; the proxy must go before the container", got)
	}

	// A failing close stops the delete.
	r2 := newRig(t)
	closer2 := &fakeCloser{host: r2.host, err: errors.New("docker down")}
	r2.svc.d.DirectAccess = closer2
	d2 := r2.create(t, "Shop", "postgres", "18")
	r2.host.reset()
	if err := r2.svc.Delete(ctx, d2.ID, "Shop", false); err == nil {
		t.Fatal("Delete succeeded though the Direct access could not be closed")
	}
	if r2.host.count("remove") != 0 {
		t.Fatalf("the Database was removed anyway: %v", r2.host.all())
	}
	if _, err := r2.store.Get(ctx, d2.ID); err != nil {
		t.Fatalf("row gone: %v", err)
	}
}
