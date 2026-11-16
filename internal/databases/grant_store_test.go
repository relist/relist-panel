package databases

import (
	"context"
	"errors"
	"testing"
	"time"
)

// insertPanelUser adds a row to users and returns its id.
func insertPanelUser(t *testing.T, s *Store, email string, isAdmin bool) int64 {
	t.Helper()
	res, err := s.db.ExecContext(context.Background(),
		"INSERT INTO users (email, is_admin, created_at) VALUES (?, ?, ?)", email, isAdmin, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestGrantStoreRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d := insertPG(t, s)
	alice := insertPanelUser(t, s, "alice@example.com", false)
	if err := s.UpsertUser(ctx, d.ID, "app", "p1"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertUser(ctx, d.ID, "web", "p2"); err != nil {
		t.Fatal(err)
	}

	if err := s.SetGrant(ctx, Grant{DatabaseID: d.ID, UserID: alice, DBUser: "app"}); err != nil {
		t.Fatal(err)
	}
	list, err := s.Grants(ctx, d.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("Grants = %+v, %v", list, err)
	}
	g := list[0]
	if g.Email != "alice@example.com" || g.DBUser != "app" || g.Level != "" || g.UserID != alice || g.DatabaseID != d.ID || g.CreatedAt.IsZero() {
		t.Fatalf("Grant = %+v", g)
	}

	// An upsert replaces the user and keeps created_at; there is one row.
	if err := s.SetGrant(ctx, Grant{DatabaseID: d.ID, UserID: alice, DBUser: "web"}); err != nil {
		t.Fatal(err)
	}
	list, err = s.Grants(ctx, d.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("Grants after upsert = %+v, %v", list, err)
	}
	if list[0].DBUser != "web" || !list[0].CreatedAt.Equal(g.CreatedAt) {
		t.Fatalf("upsert = %+v, want web and created %v", list[0], g.CreatedAt)
	}

	// A Redis grant on another Database.
	r, err := s.Insert(ctx, Database{Name: "Cache", Slug: "cache", Engine: "redis", Version: "8", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetGrant(ctx, Grant{DatabaseID: r.ID, UserID: alice, Level: LevelReadWrite}); err != nil {
		t.Fatal(err)
	}
	forUser, err := s.GrantsForUser(ctx, alice)
	if err != nil || len(forUser) != 2 {
		t.Fatalf("GrantsForUser = %+v, %v", forUser, err)
	}
	forDBUser, err := s.GrantsForDBUser(ctx, d.ID, "web")
	if err != nil || len(forDBUser) != 1 || forDBUser[0].Email != "alice@example.com" {
		t.Fatalf("GrantsForDBUser = %+v, %v", forDBUser, err)
	}
	if forDBUser, err = s.GrantsForDBUser(ctx, d.ID, "app"); err != nil || len(forDBUser) != 0 {
		t.Fatalf("GrantsForDBUser app = %+v, %v", forDBUser, err)
	}

	email, isAdmin, err := s.PanelUser(ctx, alice)
	if err != nil || email != "alice@example.com" || isAdmin {
		t.Fatalf("PanelUser = %q, %v, %v", email, isAdmin, err)
	}
	if _, _, err := s.PanelUser(ctx, alice+100); !errors.Is(err, ErrUnknownPanelUser) {
		t.Fatalf("PanelUser missing = %v, want ErrUnknownPanelUser", err)
	}

	if err := s.DeleteGrant(ctx, d.ID, alice); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGrant(ctx, d.ID, alice); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}

func TestGrantConstraints(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	d := insertPG(t, s)
	alice := insertPanelUser(t, s, "alice@example.com", false)
	if err := s.UpsertUser(ctx, d.ID, "app", "p1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGrant(ctx, Grant{DatabaseID: d.ID, UserID: alice, DBUser: "app", Level: LevelRead}); err == nil {
		t.Fatal("both db_user and level must fail")
	}
	if err := s.SetGrant(ctx, Grant{DatabaseID: d.ID, UserID: alice}); err == nil {
		t.Fatal("neither db_user nor level must fail")
	}
	if err := s.SetGrant(ctx, Grant{DatabaseID: d.ID, UserID: alice, DBUser: "ghost"}); err == nil {
		t.Fatal("a db_user outside database_users must fail")
	}
	if err := s.SetGrant(ctx, Grant{DatabaseID: d.ID, UserID: alice, Level: RedisLevel("admin")}); err == nil {
		t.Fatal("level admin must fail")
	}
	if !LevelRead.Valid() || !LevelReadWrite.Valid() || RedisLevel("admin").Valid() || RedisLevel("").Valid() {
		t.Fatal("Valid is wrong")
	}
}

func TestGrantLifecycle(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*Store, Database, int64) {
		s := newStore(t)
		d := insertPG(t, s)
		alice := insertPanelUser(t, s, "alice@example.com", false)
		if err := s.UpsertUser(ctx, d.ID, "app", "p1"); err != nil {
			t.Fatal(err)
		}
		if err := s.SetGrant(ctx, Grant{DatabaseID: d.ID, UserID: alice, DBUser: "app"}); err != nil {
			t.Fatal(err)
		}
		return s, d, alice
	}
	left := func(t *testing.T, s *Store, databaseID int64) int {
		t.Helper()
		g, err := s.Grants(ctx, databaseID)
		if err != nil {
			t.Fatal(err)
		}
		return len(g)
	}

	t.Run("panel user deleted", func(t *testing.T) {
		s, d, alice := setup(t)
		if _, err := s.db.ExecContext(ctx, "DELETE FROM users WHERE id = ?", alice); err != nil {
			t.Fatal(err)
		}
		if n := left(t, s, d.ID); n != 0 {
			t.Fatalf("grants left = %d", n)
		}
	})
	t.Run("managed user deleted", func(t *testing.T) {
		s, d, _ := setup(t)
		if err := s.DeleteUser(ctx, d.ID, "app"); err != nil {
			t.Fatal(err)
		}
		if n := left(t, s, d.ID); n != 0 {
			t.Fatalf("grants left = %d", n)
		}
	})
	t.Run("password reset keeps it", func(t *testing.T) {
		s, d, _ := setup(t)
		if err := s.UpsertUser(ctx, d.ID, "app", "p2"); err != nil {
			t.Fatal(err)
		}
		if n := left(t, s, d.ID); n != 1 {
			t.Fatalf("grants left = %d, want 1", n)
		}
	})
	t.Run("tombstone removes it", func(t *testing.T) {
		s, d, _ := setup(t)
		if err := s.Tombstone(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		if n := left(t, s, d.ID); n != 0 {
			t.Fatalf("grants left = %d", n)
		}
	})
	t.Run("database row deleted", func(t *testing.T) {
		s, d, alice := setup(t)
		if _, err := s.db.ExecContext(ctx, "DELETE FROM databases WHERE id = ?", d.ID); err != nil {
			t.Fatal(err)
		}
		g, err := s.GrantsForUser(ctx, alice)
		if err != nil || len(g) != 0 {
			t.Fatalf("GrantsForUser = %+v, %v", g, err)
		}
	})
	t.Run("GrantsForUser skips a tombstone", func(t *testing.T) {
		s, d, alice := setup(t)
		// Keep the row: tombstone by hand without the delete.
		if _, err := s.db.ExecContext(ctx, "UPDATE databases SET deleted_at = 1 WHERE id = ?", d.ID); err != nil {
			t.Fatal(err)
		}
		g, err := s.GrantsForUser(ctx, alice)
		if err != nil || len(g) != 0 {
			t.Fatalf("GrantsForUser = %+v, %v", g, err)
		}
	})
}
