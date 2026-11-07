package registryauth

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
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

func TestNormalizeHost(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"https://GHCR.io/", "ghcr.io", true},
		{"http://reg.example.com:5443", "reg.example.com:5443", true},
		{" ghcr.io ", "ghcr.io", true},
		{"index.docker.io", "docker.io", true},
		{"registry-1.docker.io", "docker.io", true},
		{"docker.io", "docker.io", true},
		{"a b", "", false},
		{"", "", false},
		{"ghcr.io/owner", "", false},
		{"reg.example.com:123456", "", false},
		{"reg.example.com:05000", "reg.example.com:5000", true},
	}
	for _, c := range cases {
		got, err := NormalizeHost(c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("NormalizeHost(%q) = %q, %v; want %q, ok=%v", c.in, got, err, c.want, c.ok)
		}
	}
}

func TestCreateDuplicateHost(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, External{Host: "ghcr.io", Username: "a", Secret: "x"}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, External{Host: "ghcr.io", Username: "b", Secret: "y"}, t0); !errors.Is(err, ErrDuplicateHost) {
		t.Fatalf("err = %v, want ErrDuplicateHost", err)
	}
}

func TestUpdateEmptySecretKeepsStored(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	e, err := s.Create(ctx, External{Host: "ghcr.io", Username: "a", Secret: "old"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	e.Username, e.Secret = "b", ""
	if err := s.Update(ctx, e, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, e.ID)
	if err != nil || got.Secret != "old" || got.Username != "b" || !got.UpdatedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("got %+v, %v", got, err)
	}
	e.Secret = "new"
	if err := s.Update(ctx, e, t0); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, e.ID); got.Secret != "new" {
		t.Fatalf("secret = %q, want new", got.Secret)
	}
}

func TestListOrderedByHost(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, h := range []string{"quay.io", "docker.io", "ghcr.io"} {
		if _, err := s.Create(ctx, External{Host: h, Username: "u", Secret: "s"}, t0); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 3 || list[0].Host != "docker.io" || list[2].Host != "quay.io" {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestSetTestResultAndDelete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	e, _ := s.Create(ctx, External{Host: "ghcr.io", Username: "u", Secret: "s"}, t0)
	if err := s.SetTestResult(ctx, e.ID, false, "unauthorized", t0); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, e.ID)
	if got.LastTestAt == nil || !got.LastTestAt.Equal(t0) || got.LastTestOK || got.LastTestError != "unauthorized" {
		t.Fatalf("got %+v", got)
	}
	if err := s.Delete(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, e.ID); err == nil {
		t.Fatal("Get after Delete succeeded")
	}
}

func TestNormalizeHostRefusesBadShapes(t *testing.T) {
	for _, in := range []string{"..", "a..b", ".ghcr.io", "ghcr.io.", "-a.io", "reg.example.com:0", "reg.example.com:65536", "reg.example.com:"} {
		if got, err := NormalizeHost(in); err == nil {
			t.Errorf("NormalizeHost(%q) = %q, want an error", in, got)
		}
	}
	for _, in := range []string{"reg.example.com:65535", "localhost:5000", "10.0.0.1:5000"} {
		if _, err := NormalizeHost(in); err != nil {
			t.Errorf("NormalizeHost(%q) = %v", in, err)
		}
	}
	_, err := NormalizeHost("ghcr.io/owner/app")
	if err == nil || !strings.Contains(err.Error(), "no path") {
		t.Fatalf("path error = %v, want it to say no path", err)
	}
}
