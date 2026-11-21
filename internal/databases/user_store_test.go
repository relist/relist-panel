package databases

import (
	"context"
	"errors"
	"testing"
)

func insertPG(t *testing.T, s *Store) Database {
	t.Helper()
	d, err := s.Insert(context.Background(), Database{Name: "Shop", Slug: "shop", Engine: "postgres", Version: "18", Password: "old"})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestUserStoreRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d := insertPG(t, s)
	if _, err := s.GetUser(ctx, d.ID, "app"); !errors.Is(err, ErrUserNotManaged) {
		t.Fatalf("GetUser missing = %v, want ErrUserNotManaged", err)
	}
	if err := s.UpsertUser(ctx, d.ID, "app", "p1"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertUser(ctx, d.ID, "web", "p2"); err != nil {
		t.Fatal(err)
	}
	u, err := s.GetUser(ctx, d.ID, "app")
	if err != nil || u.Password != "p1" || u.Name != "app" || u.DatabaseID != d.ID || u.CreatedAt.IsZero() {
		t.Fatalf("GetUser = %+v, %v", u, err)
	}
	if err := s.UpsertUser(ctx, d.ID, "app", "p3"); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.GetUser(ctx, d.ID, "app"); u.Password != "p3" {
		t.Fatalf("upsert did not replace the password: %+v", u)
	}
	list, err := s.ListUsers(ctx, d.ID)
	if err != nil || len(list) != 2 || list[0].Name != "app" || list[1].Name != "web" {
		t.Fatalf("ListUsers = %+v, %v", list, err)
	}
	if err := s.DeleteUser(ctx, d.ID, "app"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, d.ID, "app"); err != nil {
		t.Fatalf("deleting a missing row must be fine: %v", err)
	}
	if _, err := s.GetUser(ctx, d.ID, "app"); !errors.Is(err, ErrUserNotManaged) {
		t.Fatalf("after delete = %v", err)
	}
}

func TestTombstoneRemovesUsers(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d := insertPG(t, s)
	_ = s.UpsertUser(ctx, d.ID, "app", "p")
	if err := s.Tombstone(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListUsers(ctx, d.ID); len(list) != 0 {
		t.Fatalf("a tombstone keeps users: %+v", list)
	}
}

func TestDeleteDatabaseCascadesUsers(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d := insertPG(t, s)
	_ = s.UpsertUser(ctx, d.ID, "app", "p")
	if err := s.Delete(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM database_users`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("database_users rows = %d, %v", n, err)
	}
}

func TestPendingPassword(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d := insertPG(t, s)
	if err := s.PromotePending(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, d.ID); got.Password != "old" || got.PendingPassword != "" {
		t.Fatalf("a promote with no pending changed %+v", got)
	}
	if err := s.SetPendingPassword(ctx, d.ID, "new"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, d.ID); got.Password != "old" || got.PendingPassword != "new" {
		t.Fatalf("after set pending %+v", got)
	}
	if err := s.PromotePending(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, d.ID); got.Password != "new" || got.PendingPassword != "" {
		t.Fatalf("after promote %+v", got)
	}
	_ = s.SetPendingPassword(ctx, d.ID, "later")
	if err := s.ClearPending(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, d.ID); got.Password != "new" || got.PendingPassword != "" {
		t.Fatalf("after clear %+v", got)
	}
}
