package registry

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
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
