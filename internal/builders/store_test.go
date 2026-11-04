package builders

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"relist-panel/internal/database"
)

var t0 = time.Unix(1_800_000_000, 0)

type fixture struct {
	store *Store
	db    *sql.DB
	env   int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixture{store: NewStore(db), db: db}
	p := f.exec(t, `INSERT INTO projects (name, created_at, updated_at) VALUES ('p', 1, 1)`)
	f.env = f.exec(t, `INSERT INTO environments (project_id, name, created_at) VALUES (?, 'e', 1)`, p)
	return f
}

func (f *fixture) exec(t *testing.T, q string, args ...any) int64 {
	t.Helper()
	res, err := f.db.Exec(q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (f *fixture) compose(t *testing.T, name string) int64 {
	t.Helper()
	return f.exec(t, `INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at) VALUES (?, ?, '', 1, 1)`, f.env, name)
}

func (f *fixture) builder(t *testing.T, name string) Builder {
	t.Helper()
	b, err := f.store.Create(context.Background(), Builder{Name: name, CPUsMilli: 2000, MemoryMB: 4096, MaxParallelism: 4}, t0)
	if err != nil {
		t.Fatalf("Create %s: %v", name, err)
	}
	return b
}

func TestCreateGetRoundTrip(t *testing.T) {
	f := newFixture(t)
	b := f.builder(t, "large")
	got, err := f.store.Get(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "large" || got.CPUsMilli != 2000 || got.MemoryMB != 4096 || got.MaxParallelism != 4 || !got.CreatedAt.Equal(t0) {
		t.Fatalf("Get = %+v", got)
	}
	if _, err := f.store.Get(context.Background(), b.ID+1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get missing err = %v, want sql.ErrNoRows", err)
	}
}

func TestResolveFallsBackToPanelDefault(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	c := f.compose(t, "web")
	b, other := f.builder(t, "large"), f.builder(t, "small")
	resolve := func(want int64) {
		t.Helper()
		got, err := f.store.Resolve(ctx, c)
		if err != nil || got != want {
			t.Fatalf("Resolve = %d, %v; want %d", got, err, want)
		}
	}
	resolve(DockerDefault)
	if err := f.store.SetDefault(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	resolve(b.ID)
	if err := f.store.SetComposeChoice(ctx, c, Choice{Mode: ModeDocker}); err != nil {
		t.Fatal(err)
	}
	resolve(DockerDefault)
	if err := f.store.SetComposeChoice(ctx, c, Choice{Mode: ModeBuilder, BuilderID: other.ID}); err != nil {
		t.Fatal(err)
	}
	resolve(other.ID)
}

func TestComposeChoiceRoundTrip(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	c := f.compose(t, "web")
	b := f.builder(t, "large")
	got, err := f.store.ComposeChoice(ctx, c)
	if err != nil || got != (Choice{Mode: ModeDefault}) {
		t.Fatalf("no row = %+v, %v", got, err)
	}
	for _, want := range []Choice{{Mode: ModeDocker}, {Mode: ModeBuilder, BuilderID: b.ID}, {Mode: ModeDefault}} {
		if err := f.store.SetComposeChoice(ctx, c, want); err != nil {
			t.Fatalf("Set %+v: %v", want, err)
		}
		got, err := f.store.ComposeChoice(ctx, c)
		if err != nil || got != want {
			t.Fatalf("ComposeChoice = %+v, %v; want %+v", got, err, want)
		}
	}
}

func TestSetComposeChoiceUnknownBuilder(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	c := f.compose(t, "web")
	for _, bad := range []Choice{{Mode: ModeBuilder, BuilderID: 99}, {Mode: "nope"}} {
		if err := f.store.SetComposeChoice(ctx, c, bad); !errors.Is(err, ErrUnknownBuilder) {
			t.Fatalf("Set %+v err = %v, want ErrUnknownBuilder", bad, err)
		}
	}
}

func TestUsedByListsComposeNames(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	b := f.builder(t, "large")
	for _, name := range []string{"web", "api"} {
		if err := f.store.SetComposeChoice(ctx, f.compose(t, name), Choice{Mode: ModeBuilder, BuilderID: b.ID}); err != nil {
			t.Fatal(err)
		}
	}
	f.compose(t, "other")
	got, err := f.store.UsedBy(ctx, b.ID)
	if err != nil || !reflect.DeepEqual(got, []string{"api", "web"}) {
		t.Fatalf("UsedBy = %v, %v", got, err)
	}
}

func TestDeleteRestrictedByForeignKey(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	b := f.builder(t, "large")
	if err := f.store.SetComposeChoice(ctx, f.compose(t, "web"), Choice{Mode: ModeBuilder, BuilderID: b.ID}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Delete(ctx, b.ID); err == nil {
		t.Fatal("Delete of a used Builder succeeded")
	}
	unused := f.builder(t, "small")
	if err := f.store.Delete(ctx, unused.ID); err != nil {
		t.Fatalf("Delete unused: %v", err)
	}
}

func TestComposeDeleteCascadesChoice(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	b := f.builder(t, "large")
	c := f.compose(t, "web")
	if err := f.store.SetComposeChoice(ctx, c, Choice{Mode: ModeBuilder, BuilderID: b.ID}); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `DELETE FROM composes WHERE id = ?`, c)
	if err := f.store.Delete(ctx, b.ID); err != nil {
		t.Fatalf("Delete after the Compose went: %v", err)
	}
}

func TestBuildKitImageRoundTrip(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	got, err := f.store.BuildKitImage(ctx)
	if err != nil || got != "" {
		t.Fatalf("BuildKitImage = %q, %v", got, err)
	}
	ref := "moby/buildkit:v0.34.1@sha256:" + "ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12"
	if err := f.store.SetBuildKitImage(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.store.BuildKitImage(ctx); got != ref {
		t.Fatalf("BuildKitImage = %q", got)
	}
}

func TestUpdateAndLastError(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	b := f.builder(t, "large")
	b.Name, b.CPUsMilli = "huge", 4000
	if err := f.store.Update(ctx, b, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetLastError(ctx, b.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.Get(ctx, b.ID)
	if got.Name != "huge" || got.CPUsMilli != 4000 || got.LastError != "boom" || !got.UpdatedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("Get = %+v", got)
	}
	list, err := f.store.List(ctx)
	if err != nil || len(list) != 1 || list[0].ID != b.ID {
		t.Fatalf("List = %+v, %v", list, err)
	}
}
