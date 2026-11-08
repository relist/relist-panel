package authgroups

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"relist-panel/internal/database"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, _ := newStoreDB(t)
	return s
}

func newStoreDB(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db), db
}

func TestCleanName(t *testing.T) {
	got, ferr := CleanName("  team  ")
	if ferr != nil || got != "team" {
		t.Fatalf("CleanName = %q, %v", got, ferr)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("a", 65)} {
		if _, ferr := CleanName(bad); ferr == nil || ferr.Field != "name" {
			t.Errorf("CleanName(%q) = %v, want name error", bad, ferr)
		}
	}
}

func TestCheckUsername(t *testing.T) {
	if ferr := CheckUsername("alice"); ferr != nil {
		t.Fatalf("alice refused: %v", ferr)
	}
	for _, bad := range []string{"", "a:b", "a b", "ä", strings.Repeat("a", 65)} {
		if ferr := CheckUsername(bad); ferr == nil || ferr.Field != "username" {
			t.Errorf("CheckUsername(%q) = %v, want username error", bad, ferr)
		}
	}
}

func TestCheckPassword(t *testing.T) {
	for n, ok := range map[int]bool{7: false, 8: true, 72: true, 73: false} {
		ferr := CheckPassword(strings.Repeat("p", n))
		if ok && ferr != nil {
			t.Errorf("%d bytes refused: %v", n, ferr)
		}
		if !ok && (ferr == nil || ferr.Field != "password") {
			t.Errorf("%d bytes = %v, want password error", n, ferr)
		}
	}
}

func TestCreateWithUsersAndCounts(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	g, err := s.Create(ctx, "Staging", []UserInput{{"alice", "password-one"}})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if list[0].ID != g.ID || list[0].UserCount != 1 || list[0].DomainCount != 0 {
		t.Errorf("List[0] = %+v", list[0])
	}
	users, err := s.Users(ctx, g.ID)
	if err != nil || len(users) != 1 {
		t.Fatalf("Users = %+v, %v", users, err)
	}
	if users[0].Password != "password-one" {
		t.Errorf("Password = %q", users[0].Password)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(users[0].Hash), []byte("password-one")); err != nil {
		t.Errorf("hash does not verify: %v", err)
	}
}

func TestDuplicateNameIgnoresCase(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "Staging", nil); err != nil {
		t.Fatal(err)
	}
	_, err := s.Create(ctx, "staging", nil)
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.Field != "name" {
		t.Fatalf("err = %v, want name ConflictError", err)
	}
}

func TestDuplicateUsername(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	g, err := s.Create(ctx, "Staging", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddUser(ctx, g.ID, UserInput{"alice", "password-one"}); err != nil {
		t.Fatal(err)
	}
	_, err = s.AddUser(ctx, g.ID, UserInput{"alice", "password-two"})
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.Field != "username" {
		t.Fatalf("err = %v, want username ConflictError", err)
	}
}

func TestUpdateUserUsernameOnlyKeepsHash(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	t0 := time.Unix(1000, 0)
	s.now = func() time.Time { return t0 }
	g, _ := s.Create(ctx, "Staging", nil)
	u, err := s.AddUser(ctx, g.ID, UserInput{"alice", "password-one"})
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return t0.Add(time.Hour) }
	name := "al"
	got, err := s.UpdateUser(ctx, g.ID, u.ID, &name, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "al" || got.Hash != u.Hash || got.Password != "password-one" {
		t.Errorf("after rename = %+v, before = %+v", got, u)
	}
	if !got.UpdatedAt.After(u.UpdatedAt) {
		t.Errorf("UpdatedAt did not move: %v -> %v", u.UpdatedAt, got.UpdatedAt)
	}
	grp, _ := s.Get(ctx, g.ID)
	if !grp.UpdatedAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("group UpdatedAt = %v", grp.UpdatedAt)
	}
}

func TestUpdateUserPasswordRehashes(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	g, _ := s.Create(ctx, "Staging", nil)
	u, _ := s.AddUser(ctx, g.ID, UserInput{"alice", "password-one"})
	pw := "password-two"
	got, err := s.UpdateUser(ctx, g.ID, u.ID, nil, &pw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Hash == u.Hash {
		t.Error("hash did not change")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(got.Hash), []byte(pw)); err != nil {
		t.Errorf("new hash does not verify: %v", err)
	}
}

func TestUserNotFound(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	g, _ := s.Create(ctx, "Staging", nil)
	other, _ := s.Create(ctx, "Other", nil)
	u, _ := s.AddUser(ctx, g.ID, UserInput{"alice", "password-one"})
	if err := s.DeleteUser(ctx, other.ID, u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteUser via wrong group = %v", err)
	}
	if _, err := s.Get(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get = %v", err)
	}
	if err := s.DeleteUser(ctx, g.ID, u.ID); err != nil {
		t.Errorf("DeleteUser = %v", err)
	}
}

func TestDeleteRefusedWhileUsed(t *testing.T) {
	s, db := newStoreDB(t)
	ctx := context.Background()
	g, err := s.Create(ctx, "Staging", []UserInput{{"alice", "password-one"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO projects (id, name, description, created_at, updated_at) VALUES (1, 'Shop', '', 1, 1)`,
		`INSERT INTO environments (id, project_id, name, created_at) VALUES (2, 1, 'prod', 1)`,
		`INSERT INTO composes (id, environment_id, name, yaml_content, created_at, updated_at) VALUES (3, 2, 'web', '', 1, 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO domains (compose_id, host, service, port, created_at, updated_at, auth_group_id) VALUES (3, 'a.example.com', 'web', 80, 1, 1, ?)`, g.ID); err != nil {
		t.Fatal(err)
	}
	used, err := s.Delete(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(used) != 1 {
		t.Fatalf("used = %+v", used)
	}
	u := used[0]
	if u.Host != "a.example.com" || u.Path != "/" || u.ProjectName != "Shop" || u.EnvironmentName != "prod" || u.ComposeName != "web" ||
		u.ProjectID != 1 || u.EnvironmentID != 2 || u.ComposeID != 3 {
		t.Errorf("used[0] = %+v", u)
	}
	if _, err := s.Get(ctx, g.ID); err != nil {
		t.Fatalf("group removed while used: %v", err)
	}
	if got, _ := s.Get(ctx, g.ID); got.DomainCount != 1 {
		t.Errorf("DomainCount = %d", got.DomainCount)
	}

	// Deleting the Compose cascades to the Domain, freeing the group.
	if _, err := db.Exec(`DELETE FROM composes WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	used, err = s.Delete(ctx, g.ID)
	if err != nil || len(used) != 0 {
		t.Fatalf("Delete = %+v, %v", used, err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM auth_group_users`).Scan(&n); err != nil || n != 0 {
		t.Errorf("users left = %d, %v", n, err)
	}
	if _, err := s.Get(ctx, g.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete = %v", err)
	}
}

func TestRenameAndOptions(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	b, _ := s.Create(ctx, "b", nil)
	a, _ := s.Create(ctx, "a", nil)
	if _, err := s.Rename(ctx, b.ID, "A"); err == nil {
		t.Error("rename onto existing name (case-insensitive) accepted")
	}
	if got, err := s.Rename(ctx, b.ID, "c"); err != nil || got.Name != "c" {
		t.Fatalf("Rename = %+v, %v", got, err)
	}
	opts, err := s.Options(ctx)
	if err != nil || len(opts) != 2 || opts[0].ID != a.ID || opts[1].Name != "c" {
		t.Fatalf("Options = %+v, %v", opts, err)
	}
	if ok, _ := s.Exists(ctx, a.ID); !ok {
		t.Error("Exists(a) = false")
	}
	if ok, _ := s.Exists(ctx, 999); ok {
		t.Error("Exists(999) = true")
	}
}
