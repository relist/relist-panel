package certificates

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
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

func parsed(t *testing.T, sans ...string) Parsed {
	t.Helper()
	p := newPair(t, pairOptions{sans: sans})
	got, ferr := Parse(p.cert, p.key, testNow)
	if ferr != nil {
		t.Fatalf("Parse: %v", ferr)
	}
	return got
}

func TestStoreCreateListOrder(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "b", parsed(t, "b.example.com")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "a", parsed(t, "a.example.com", "*.a.example.com")); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "a" || list[1].Name != "b" {
		t.Fatalf("List = %+v", list)
	}
	if strings.Join(list[0].SANs, ",") != "a.example.com,*.a.example.com" {
		t.Errorf("SANs = %v", list[0].SANs)
	}
	if !strings.Contains(list[0].PrivateKeyPEM, "PRIVATE KEY") || !strings.Contains(list[0].CertificatePEM, "CERTIFICATE") {
		t.Error("PEM did not round-trip")
	}
}

func TestStoreDuplicateName(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "a", parsed(t, "x.example.com")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "a", parsed(t, "x.example.com")); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate: err = %v, want ErrNameTaken", err)
	}
	if _, err := s.Create(ctx, "A", parsed(t, "x.example.com")); err != nil {
		t.Fatalf("names are case-sensitive: %v", err)
	}
}

func TestStoreReplace(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	clock := time.Unix(1000, 0)
	s.now = func() time.Time { return clock }
	created, err := s.Create(ctx, "a", parsed(t, "old.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Minute)
	next := parsed(t, "new.example.com")
	next.NotAfter = created.NotAfter.Add(24 * time.Hour)
	got, err := s.Replace(ctx, created.ID, next)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "a" || !got.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("name/createdAt changed: %+v", got)
	}
	if !got.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want after %v", got.UpdatedAt, created.UpdatedAt)
	}
	if strings.Join(got.SANs, ",") != "new.example.com" || !got.NotAfter.Equal(next.NotAfter) {
		t.Errorf("derived fields not refreshed: %+v", got)
	}
	list, _ := s.List(ctx)
	if list[0].CertificatePEM != next.CertificatePEM {
		t.Error("List still holds the old certificate")
	}
	if _, err := s.Replace(ctx, 999, next); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("unknown id: err = %v", err)
	}
}

func TestStoreDelete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	c, err := s.Create(ctx, "a", parsed(t, "x.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(ctx); len(list) != 0 {
		t.Errorf("List = %+v", list)
	}
	if err := s.Delete(ctx, c.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("unknown id: err = %v", err)
	}
}

func TestCleanName(t *testing.T) {
	if got, ferr := CleanName("  my-cert.1 "); ferr != nil || got != "my-cert.1" {
		t.Errorf("CleanName = %q, %v", got, ferr)
	}
	for _, bad := range []string{"", strings.Repeat("a", 65), "my cert", "cert/1"} {
		if _, ferr := CleanName(bad); ferr == nil || ferr.Field != "name" {
			t.Errorf("CleanName(%q) = %v, want a name error", bad, ferr)
		}
	}
}
