package registry

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"relist-panel/internal/database"
)

var t0 = time.Unix(1_800_000_000, 0)

func newStore(t *testing.T) *Store {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db)
}

func TestSettingsDefaultRow(t *testing.T) {
	s := newStore(t)
	got, err := s.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != (Settings{TLS: TLSLetsEncrypt}) {
		t.Fatalf("default = %+v", got)
	}
}

func TestSaveSettingsRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	want := Settings{Enabled: true, Hostname: "registry.example.com", TLS: TLSCertificate, SystemSecret: "sec", SystemHash: "$2a$hash", LastError: ""}
	if err := s.SaveSettings(ctx, want); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLastError(ctx, "pull failed"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Settings(ctx)
	want.LastError = "pull failed"
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	want.Hostname = ""
	if err := s.SaveSettings(ctx, want); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Settings(ctx); got.Hostname != "" {
		t.Fatalf("hostname = %q, want empty", got.Hostname)
	}
}

func TestCredentialDuplicateName(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.CreateCredential(ctx, Credential{Name: "github-ci", Hash: "h", CreatedBy: "a@b.c", CreatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCredential(ctx, Credential{Name: "github-ci", Hash: "h2", CreatedBy: "a@b.c", CreatedAt: t0}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("err = %v, want ErrDuplicateName", err)
	}
}

func TestCredentialsOrderedByName(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, n := range []string{"zeta", "alpha"} {
		if _, err := s.CreateCredential(ctx, Credential{Name: n, Hash: "h", CreatedBy: "a@b.c", CreatedAt: t0}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.Credentials(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "alpha" || !list[1].CreatedAt.Equal(t0) || list[1].CreatedBy != "a@b.c" {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestDeleteCredentialReturnsIt(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	c, _ := s.CreateCredential(ctx, Credential{Name: "ci", Hash: "h", CreatedBy: "a@b.c", CreatedAt: t0})
	got, err := s.DeleteCredential(ctx, c.ID)
	if err != nil || got.Name != "ci" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := s.DeleteCredential(ctx, c.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second delete err = %v, want sql.ErrNoRows", err)
	}
}

func TestCleanupDefaults(t *testing.T) {
	s := newStore(t)
	c, err := s.CleanupSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled || c.Keep != 10 || !slices.Equal(c.Days, []string{"daily"}) || c.Minute != 180 || c.LastRunAt != nil {
		t.Fatalf("defaults = %+v", c)
	}
	at := time.Unix(1_800_000_000, 0)
	c = CleanupSettings{Enabled: false, Keep: 3, Days: []string{"mon", "thu"}, Minute: 90, LastRunAt: &at}
	if err := s.SaveCleanupSettings(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	got, _ := s.CleanupSettings(context.Background())
	if got.Enabled || got.Keep != 3 || !slices.Equal(got.Days, []string{"mon", "thu"}) || got.Minute != 90 || got.LastRunAt == nil || !got.LastRunAt.Equal(at) {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestRunsKeepsLastTen(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	var last int64
	for i := 0; i < 12; i++ {
		id, err := s.StartRun(ctx, time.Unix(int64(1000+i), 0), "schedule")
		if err != nil {
			t.Fatal(err)
		}
		last = id
	}
	if err := s.FinishRun(ctx, last, time.Unix(2000, 0), RunOK, []string{"app:1"}, 4096, "gc log"); err != nil {
		t.Fatal(err)
	}
	runs, err := s.Runs(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 10 || runs[0].ID != last {
		t.Fatalf("runs = %d, first %d, want 10 newest first", len(runs), runs[0].ID)
	}
	r := runs[0]
	if r.Result != RunOK || r.FreedBytes == nil || *r.FreedBytes != 4096 || !slices.Equal(r.Deleted, []string{"app:1"}) || r.Log != "gc log" || r.FinishedAt == nil {
		t.Fatalf("finished run = %+v", r)
	}
	if runs[1].Result != RunRunning || runs[1].FinishedAt != nil || runs[1].Deleted == nil {
		t.Fatalf("running run = %+v", runs[1])
	}
}

func TestGCRunningRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if id, err := s.GCRunning(ctx); err != nil || id != 0 {
		t.Fatalf("initial = %d, %v", id, err)
	}
	if err := s.SetGCRunning(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.GCRunning(ctx); id != 7 {
		t.Fatalf("set = %d", id)
	}
	_ = s.SetGCRunning(ctx, 0)
	if id, _ := s.GCRunning(ctx); id != 0 {
		t.Fatalf("cleared = %d", id)
	}
}
