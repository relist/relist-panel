package directaccess

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"relist-panel/internal/database"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db)
}

func TestStoreRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	exp := time.Unix(1900000000, 0)
	opened := time.Unix(1899990000, 0)
	id, err := s.Insert(ctx, Access{
		Target:    Target{Kind: KindDatabase, ID: 7, Port: 5432},
		Channel:   ChannelHostPort,
		HostPort:  HostPort{Bind: "0.0.0.0", HostPort: 15432},
		ExpiresAt: &exp,
		OpenedBy:  3,
		OpenedAt:  opened,
	})
	if err != nil || id == 0 {
		t.Fatalf("Insert = %d, %v", id, err)
	}
	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.Target != (Target{KindDatabase, 7, 5432}) || got.Channel != ChannelHostPort ||
		got.HostPort != (HostPort{"0.0.0.0", 15432}) || got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) ||
		got.OpenedBy != 3 || !got.OpenedAt.Equal(opened) {
		t.Fatalf("Get = %+v", got)
	}

	// A nil ExpiresAt stays nil.
	id2, err := s.Insert(ctx, Access{Target: Target{KindDatabase, 8, 6379}, Channel: ChannelHostPort, HostPort: HostPort{Bind: "127.0.0.1"}, OpenedAt: opened})
	if err != nil {
		t.Fatal(err)
	}
	got2, _ := s.Get(ctx, id2)
	if got2.ExpiresAt != nil {
		t.Fatalf("ExpiresAt = %v, want nil", got2.ExpiresAt)
	}
}

func TestStoreSetsChangeOneColumn(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	exp := time.Unix(1900000000, 0)
	id, _ := s.Insert(ctx, Access{Target: Target{KindDatabase, 7, 5432}, Channel: ChannelHostPort, HostPort: HostPort{Bind: "127.0.0.1"}, ExpiresAt: &exp, OpenedBy: 1, OpenedAt: time.Unix(1, 0)})
	before, _ := s.Get(ctx, id)

	if err := s.SetHostPort(ctx, id, 20000); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, id)
	if got.HostPort != (HostPort{"127.0.0.1", 20000}) || !got.ExpiresAt.Equal(*before.ExpiresAt) || got.Target != before.Target {
		t.Fatalf("after SetHostPort: %+v", got)
	}

	later := time.Unix(1900009000, 0)
	if err := s.SetExpiry(ctx, id, later); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, id)
	if !got.ExpiresAt.Equal(later) || got.HostPort.HostPort != 20000 || got.HostPort.Bind != "127.0.0.1" {
		t.Fatalf("after SetExpiry: %+v", got)
	}
}

func TestStoreForTargetListDelete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mk := func(kind string, tid int64) int64 {
		id, err := s.Insert(ctx, Access{Target: Target{kind, tid, 5432}, Channel: ChannelHostPort, HostPort: HostPort{Bind: "127.0.0.1"}, OpenedAt: time.Unix(1, 0)})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a := mk(KindDatabase, 7)
	mk(KindDatabase, 8)
	mk("service", 7)

	got, err := s.ForTarget(ctx, KindDatabase, 7)
	if err != nil || len(got) != 1 || got[0].ID != a {
		t.Fatalf("ForTarget = %+v, %v", got, err)
	}
	all, err := s.List(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("List = %d rows, %v", len(all), err)
	}
	if err := s.Delete(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, a); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, 9999); err != nil {
		t.Fatalf("Delete missing = %v, want nil", err)
	}
}
