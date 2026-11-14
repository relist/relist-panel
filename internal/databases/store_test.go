package databases

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

func TestStoreInsertGetList(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	b, err := s.Insert(ctx, Database{Name: "Blog", Slug: "blog", Engine: "mysql", Version: "8.4", Password: "p2", MemoryMB: 512, CPUs: 1.5, Desired: DesiredStopped, LastError: "boom"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Insert(ctx, Database{Name: "Alpha", Slug: "alpha", Engine: "postgres", Version: "18", Password: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == 0 || b.ID == 0 || a.ID == b.ID || a.CreatedAt.IsZero() || a.UpdatedAt.IsZero() {
		t.Fatalf("ids/timestamps: %+v %+v", a, b)
	}
	got, err := s.Get(ctx, b.ID)
	if err != nil || got != b {
		t.Fatalf("Get = %+v, %v; want %+v", got, err, b)
	}
	if b.Name != "Blog" || b.Slug != "blog" || b.Engine != "mysql" || b.Version != "8.4" || b.Password != "p2" ||
		b.MemoryMB != 512 || b.CPUs != 1.5 || b.Desired != DesiredStopped || b.Initialized || b.LastError != "boom" {
		t.Fatalf("round trip lost a field: %+v", b)
	}
	if a.Desired != DesiredRunning {
		t.Fatalf("default desired = %q", a.Desired)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "Alpha" || list[1].Name != "Blog" {
		t.Fatalf("List = %+v, %v", list, err)
	}
}

func TestStoreDuplicateName(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Insert(ctx, Database{Name: "Shop", Slug: "shop", Engine: "redis", Version: "8", Password: "x"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Insert(ctx, Database{Name: "shop", Slug: "shop-2", Engine: "redis", Version: "8", Password: "x"})
	if !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("err = %v, want ErrDuplicateName", err)
	}
}

func TestStoreGetMissing(t *testing.T) {
	if _, err := newStore(t).Get(context.Background(), 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestStoreSetters(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	clock := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	d, _ := s.Insert(ctx, Database{Name: "A", Slug: "a", Engine: "redis", Version: "8", Password: "x"})
	last := d.UpdatedAt
	step := func(name string, err error) Database {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, _ := s.Get(ctx, d.ID)
		if !got.UpdatedAt.After(last) {
			t.Fatalf("%s: UpdatedAt did not advance", name)
		}
		last = got.UpdatedAt
		return got
	}
	if got := step("rename", s.Rename(ctx, d.ID, "B", "b")); got.Name != "B" || got.Slug != "b" {
		t.Fatalf("rename: %+v", got)
	}
	if got := step("desired", s.SetDesired(ctx, d.ID, DesiredStopped)); got.Desired != DesiredStopped {
		t.Fatalf("desired: %+v", got)
	}
	if got := step("initialized", s.SetInitialized(ctx, d.ID)); !got.Initialized {
		t.Fatalf("initialized: %+v", got)
	}
	if got := step("last error", s.SetLastError(ctx, d.ID, "bad")); got.LastError != "bad" {
		t.Fatalf("last error: %+v", got)
	}
	if err := s.Delete(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDesired(ctx, d.ID, DesiredRunning); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete err = %v", err)
	}
}

// Two Databases answering to one readable alias would split connections
// between them, so the store refuses a repeated slug; the fallback "x" has no
// alias and may repeat.
func TestStoreSlugUniqueExceptFallback(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ins := func(name, slug string) error {
		_, err := s.Insert(ctx, Database{Name: name, Slug: slug, Engine: "redis", Version: "8", Password: "x"})
		return err
	}
	if err := ins("Shop", "shop"); err != nil {
		t.Fatal(err)
	}
	if err := ins("Shop!", "shop"); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("repeated slug err = %v, want ErrDuplicateName", err)
	}
	if err := ins("ฐาน", "x"); err != nil {
		t.Fatal(err)
	}
	if err := ins("ข้อมูล", "x"); err != nil {
		t.Fatalf("second fallback slug: %v", err)
	}
	other, err := s.Insert(ctx, Database{Name: "Blog", Slug: "blog", Engine: "redis", Version: "8", Password: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(ctx, other.ID, "Shop?", "shop"); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("rename onto a taken slug err = %v", err)
	}
}
