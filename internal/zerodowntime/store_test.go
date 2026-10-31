package zerodowntime

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"relist-panel/internal/database"
)

func newStore(t *testing.T) (*Store, *sql.DB, int64) {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	exec := func(q string, args ...any) int64 {
		res, err := db.Exec(q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	p := exec(`INSERT INTO projects (name, created_at, updated_at) VALUES ('p', 1, 1)`)
	e := exec(`INSERT INTO environments (project_id, name, created_at) VALUES (?, 'e', 1)`, p)
	c := exec(`INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at) VALUES (?, 'c', '', 1, 1)`, e)
	return NewStore(db), db, c
}

func TestSetAndEnabled(t *testing.T) {
	s, _, c := newStore(t)
	ctx, now := context.Background(), time.Unix(1000, 0)
	for _, svc := range []string{"web", "api", "api"} {
		if err := s.Set(ctx, c, svc, true, now); err != nil {
			t.Fatalf("Set %s on: %v", svc, err)
		}
	}
	got, err := s.Enabled(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"api", "web"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Enabled = %v, want %v", got, want)
	}
	if err := s.Set(ctx, c, "web", false, now); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Enabled(ctx, c); !reflect.DeepEqual(got, []string{"api"}) {
		t.Fatalf("Enabled after off = %v", got)
	}
}

func TestRowsGoWithCompose(t *testing.T) {
	s, db, c := newStore(t)
	ctx := context.Background()
	if err := s.Set(ctx, c, "web", true, time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM composes WHERE id = ?`, c); err != nil {
		t.Fatal(err)
	}
	got, err := s.Enabled(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Enabled after compose delete = %v", got)
	}
}
