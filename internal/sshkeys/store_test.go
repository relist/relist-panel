package sshkeys

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"relist-panel/internal/database"
)

// newDB opens a migrated database holding project p, environment e and the
// composes a and b, and returns it with the compose ids.
func newDB(t *testing.T) (*sql.DB, int64, int64) {
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
	a := exec(`INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at) VALUES (?, 'a', '', 1, 1)`, e)
	b := exec(`INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at) VALUES (?, 'b', '', 1, 1)`, e)
	return db, a, b
}

func useKey(t *testing.T, db *sql.DB, composeID, keyID int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO compose_sources (compose_id, kind, ssh_key_id, last_test_at, last_test_ok, created_at, updated_at)
		VALUES (?, 'git', ?, 5, 1, 1, 1)`, composeID, keyID); err != nil {
		t.Fatalf("attach key: %v", err)
	}
}

func TestCreateTrimsAndGenerates(t *testing.T) {
	db, _, _ := newDB(t)
	got, err := NewStore(db).Create(context.Background(), "  ci  ")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Name != "ci" {
		t.Errorf("Name = %q, want ci", got.Name)
	}
	if !strings.HasPrefix(got.PublicKey, "ssh-ed25519 ") || !strings.HasSuffix(got.PublicKey, " relist-ci") {
		t.Errorf("PublicKey = %q", got.PublicKey)
	}
	if !strings.Contains(got.PrivateKey, "OPENSSH PRIVATE KEY") {
		t.Errorf("PrivateKey = %q", got.PrivateKey)
	}
	if got.ID == 0 || got.CreatedAt.IsZero() || len(got.UsedBy) != 0 {
		t.Errorf("key = %+v", got)
	}
}

func TestCreateRejectsBlankAndDuplicate(t *testing.T) {
	db, _, _ := newDB(t)
	s := NewStore(db)
	ctx := context.Background()
	if _, err := s.Create(ctx, "  "); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("blank err = %v, want ErrNameRequired", err)
	}
	if _, err := s.Create(ctx, "CI"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "ci"); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate err = %v, want ErrNameTaken", err)
	}
}

func TestNamesRejectControlCharactersAndOverlongNames(t *testing.T) {
	db, _, _ := newDB(t)
	s := NewStore(db)
	ctx := context.Background()
	k, err := s.Create(ctx, strings.Repeat("é", 100))
	if err != nil {
		t.Fatalf("100-rune name: %v", err)
	}
	for _, name := range []string{"a\nb", "tab\there", strings.Repeat("x", 101)} {
		if _, err := s.Create(ctx, name); !errors.Is(err, ErrNameInvalid) {
			t.Errorf("Create(%q) err = %v, want ErrNameInvalid", name, err)
		}
		if _, err := s.Rename(ctx, k.ID, name); !errors.Is(err, ErrNameInvalid) {
			t.Errorf("Rename(%q) err = %v, want ErrNameInvalid", name, err)
		}
	}
}

func TestRename(t *testing.T) {
	db, _, _ := newDB(t)
	s := NewStore(db)
	ctx := context.Background()
	k, err := s.Create(ctx, "one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Two"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Rename(ctx, k.ID, " three ")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if got.Name != "three" || got.PublicKey != k.PublicKey {
		t.Fatalf("renamed = %+v, want name three and the same public key", got)
	}
	if again, _ := s.Get(ctx, k.ID); again.Name != "three" {
		t.Fatalf("Get after rename = %q", again.Name)
	}
	if _, err := s.Rename(ctx, k.ID, "tWO"); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("rename onto taken err = %v, want ErrNameTaken", err)
	}
	if _, err := s.Rename(ctx, k.ID, ""); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("blank rename err = %v, want ErrNameRequired", err)
	}
	if _, err := s.Rename(ctx, 999, "free"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown id err = %v, want sql.ErrNoRows", err)
	}
}

func TestListUsedBy(t *testing.T) {
	db, a, b := newDB(t)
	s := NewStore(db)
	ctx := context.Background()
	k, err := s.Create(ctx, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "unused"); err != nil {
		t.Fatal(err)
	}
	useKey(t, db, b, k.ID)
	useKey(t, db, a, k.ID)

	keys, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 || keys[0].Name != "ci" || keys[1].Name != "unused" {
		t.Fatalf("List = %+v, want ci then unused", keys)
	}
	var projectID, envID int64
	if err := db.QueryRow(`SELECT p.id, e.id FROM projects p JOIN environments e ON e.project_id = p.id`).Scan(&projectID, &envID); err != nil {
		t.Fatal(err)
	}
	want := []Use{
		{ProjectID: projectID, ProjectName: "p", EnvironmentID: envID, ComposeID: a, ComposeName: "a"},
		{ProjectID: projectID, ProjectName: "p", EnvironmentID: envID, ComposeID: b, ComposeName: "b"},
	}
	if !reflect.DeepEqual(keys[0].UsedBy, want) {
		t.Fatalf("UsedBy = %+v, want %+v", keys[0].UsedBy, want)
	}
	if len(keys[1].UsedBy) != 0 {
		t.Fatalf("unused UsedBy = %+v", keys[1].UsedBy)
	}
	got, err := s.Get(ctx, k.ID)
	if err != nil || !reflect.DeepEqual(got.UsedBy, want) {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if _, err := s.Get(ctx, 999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get unknown err = %v, want sql.ErrNoRows", err)
	}
}

func TestDeleteDetachesSources(t *testing.T) {
	db, a, _ := newDB(t)
	s := NewStore(db)
	ctx := context.Background()
	k, err := s.Create(ctx, "ci")
	if err != nil {
		t.Fatal(err)
	}
	useKey(t, db, a, k.ID)

	if err := s.Delete(ctx, k.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	var keyID, testAt, testOK sql.NullInt64
	if err := db.QueryRow(`SELECT ssh_key_id, last_test_at, last_test_ok FROM compose_sources WHERE compose_id = ?`, a).
		Scan(&keyID, &testAt, &testOK); err != nil {
		t.Fatal(err)
	}
	if keyID.Valid || testAt.Valid || testOK.Valid {
		t.Fatalf("source = %v %v %v, want all NULL", keyID, testAt, testOK)
	}
	if err := s.Delete(ctx, k.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second Delete err = %v, want sql.ErrNoRows", err)
	}
}

func addDestination(t *testing.T, db *sql.DB, name string, keyID int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO backup_destinations (name, type, config, secret, ssh_key_id, created_at, updated_at)
		VALUES (?, 'sftp', '{}', '', ?, 1, 1)`, name, keyID); err != nil {
		t.Fatalf("add destination: %v", err)
	}
}

// Review Focus 5: a key a Backup destination uses is never deleted.
func TestDeleteRefusedWhileDestinationUsesKey(t *testing.T) {
	db, a, _ := newDB(t)
	s := NewStore(db)
	ctx := context.Background()
	k, err := s.Create(ctx, "ci")
	if err != nil {
		t.Fatal(err)
	}
	useKey(t, db, a, k.ID)
	addDestination(t, db, "zeta", k.ID)
	addDestination(t, db, "alpha", k.ID)
	err = s.Delete(ctx, k.ID)
	var inUse ErrKeyInUse
	if !errors.As(err, &inUse) || !reflect.DeepEqual(inUse.Destinations, []string{"alpha", "zeta"}) {
		t.Fatalf("Delete err = %v", err)
	}
	if inUse.Error() != "used by Backup destinations: alpha, zeta" {
		t.Fatalf("text = %q", inUse.Error())
	}
	if _, err := s.Get(ctx, k.ID); err != nil {
		t.Fatalf("the key is gone: %v", err)
	}
	// Nothing was detached either: the refusal comes before any change.
	var keyID sql.NullInt64
	if err := db.QueryRow(`SELECT ssh_key_id FROM compose_sources WHERE compose_id = ?`, a).Scan(&keyID); err != nil || !keyID.Valid {
		t.Fatalf("source key = %v, %v", keyID, err)
	}
}

func TestListShowsDestinationUse(t *testing.T) {
	db, _, _ := newDB(t)
	s := NewStore(db)
	ctx := context.Background()
	k, _ := s.Create(ctx, "ci")
	other, _ := s.Create(ctx, "unused")
	addDestination(t, db, "offsite", k.ID)
	keys, err := s.List(ctx)
	if err != nil || len(keys) != 2 {
		t.Fatalf("List = %+v, %v", keys, err)
	}
	if !reflect.DeepEqual(keys[0].UsedByDestinations, []string{"offsite"}) || len(keys[1].UsedByDestinations) != 0 || keys[1].ID != other.ID {
		t.Fatalf("UsedByDestinations = %v / %v", keys[0].UsedByDestinations, keys[1].UsedByDestinations)
	}
	got, err := s.Get(ctx, k.ID)
	if err != nil || !reflect.DeepEqual(got.UsedByDestinations, []string{"offsite"}) {
		t.Fatalf("Get = %+v, %v", got, err)
	}
}
