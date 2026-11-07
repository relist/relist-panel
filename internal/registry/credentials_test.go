package registry

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestCreateCredentialStoresHashOnly(t *testing.T) {
	f := newService(t)
	f.enable(t)
	ctx := context.Background()
	c, token, err := f.svc.CreateCredential(ctx, "github-ci", "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 43 || c.Name != "github-ci" || c.CreatedBy != "admin@example.com" {
		t.Fatalf("credential = %+v, token = %q", c, token)
	}
	if bcrypt.CompareHashAndPassword([]byte(c.Hash), []byte(token)) != nil {
		t.Fatal("hash does not match the token")
	}
	var leaked int
	db := f.store.db
	if err := db.QueryRow(`SELECT COUNT(*) FROM registry_credentials WHERE hash = ? OR name = ? OR created_by = ?`, token, token, token).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("token stored: %d, %v", leaked, err)
	}
	htpasswd, _ := os.ReadFile(filepath.Join(f.files.Dir, "htpasswd"))
	if !strings.Contains(string(htpasswd), "github-ci:"+c.Hash+"\n") || strings.Contains(string(htpasswd), token) {
		t.Fatalf("htpasswd = %q", htpasswd)
	}
}

func TestCreateRejectsReservedName(t *testing.T) {
	f := newService(t)
	ctx := context.Background()
	for _, name := range []string{"relist-panel", "", "Has Space", "UPPER", "-lead", strings.Repeat("a", 41), "a:b"} {
		if _, _, err := f.svc.CreateCredential(ctx, name, "a@b.c"); !errors.Is(err, ErrInvalidName) {
			t.Errorf("%q: err = %v, want ErrInvalidName", name, err)
		}
	}
	if _, _, err := f.svc.CreateCredential(ctx, "ci_1-x", "a@b.c"); err != nil {
		t.Fatalf("valid name: %v", err)
	}
	if _, _, err := f.svc.CreateCredential(ctx, "ci_1-x", "a@b.c"); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("duplicate: err = %v", err)
	}
}

func TestDeleteRewritesHtpasswd(t *testing.T) {
	f := newService(t)
	f.enable(t)
	ctx := context.Background()
	c, _, err := f.svc.CreateCredential(ctx, "ci", "a@b.c")
	if err != nil {
		t.Fatal(err)
	}
	f.host.calls = nil
	if _, err := f.svc.DeleteCredential(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	htpasswd, _ := os.ReadFile(filepath.Join(f.files.Dir, "htpasswd"))
	if strings.Contains(string(htpasswd), "ci:") || !strings.HasPrefix(string(htpasswd), "relist-panel:") {
		t.Fatalf("htpasswd = %q", htpasswd)
	}
	for _, call := range f.host.calls {
		if call == "Restart" || call == "Create" {
			t.Fatalf("revoking restarted the registry: %v", f.host.calls)
		}
	}
	if _, err := f.svc.DeleteCredential(ctx, c.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second delete: %v", err)
	}
}

// A credential whose login could not be written is not kept: its token was
// never shown, so it could never be used.
func TestCreateCredentialRollsBackWhenFilesFail(t *testing.T) {
	f := newService(t)
	f.enable(t)
	ctx := context.Background()
	if err := os.RemoveAll(f.files.Dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.files.Dir, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.CreateCredential(ctx, "github-ci", "admin"); err == nil {
		t.Fatal("create succeeded with an unwritable htpasswd")
	}
	if creds, _ := f.store.Credentials(ctx); len(creds) != 0 {
		t.Fatalf("credentials = %+v, want none kept", creds)
	}
}
