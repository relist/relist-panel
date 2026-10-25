package sources

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"relist-panel/internal/database"
	"relist-panel/internal/sshkeys"
)

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

func TestSourceSaveGetRoundTrip(t *testing.T) {
	db, a, b := newDB(t)
	s := NewStore(db)
	ctx := context.Background()
	in := Source{
		ComposeID: a, Kind: KindGit, RepoURL: "git@github.com:o/r.git", Branch: "main", Submodules: true,
		CreatedAt: time.Unix(100, 0), UpdatedAt: time.Unix(200, 0),
	}
	if err := s.Save(ctx, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Get(ctx, a)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Kind != KindGit || got.RepoURL != in.RepoURL || got.Branch != "main" || !got.Submodules ||
		got.SSHKeyID != nil || got.PrivateKey != "" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if got.LastTestAt != nil || got.LastTestOK != nil || got.UploadedAt != nil {
		t.Fatalf("nullable fields should be nil: %+v", got)
	}
	if !got.CreatedAt.Equal(in.CreatedAt) || !got.UpdatedAt.Equal(in.UpdatedAt) {
		t.Fatalf("timestamps = %v/%v", got.CreatedAt, got.UpdatedAt)
	}
	if _, err := s.Get(ctx, b); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get other compose err = %v, want sql.ErrNoRows", err)
	}

	// Save replaces, and RecordTest sets the test fields.
	in.Branch = "dev"
	if err := s.Save(ctx, in); err != nil {
		t.Fatalf("Save replace: %v", err)
	}
	at := time.Unix(300, 0)
	if err := s.RecordTest(ctx, a, true, "abc123", at); err != nil {
		t.Fatalf("RecordTest: %v", err)
	}
	got, _ = s.Get(ctx, a)
	if got.Branch != "dev" || got.HeadSHA != "abc123" || got.LastTestOK == nil || !*got.LastTestOK ||
		got.LastTestAt == nil || !got.LastTestAt.Equal(at) {
		t.Fatalf("after RecordTest: %+v", got)
	}
	if err := s.Delete(ctx, a); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, a); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get after Delete err = %v", err)
	}
}

func TestGetJoinsTheChosenKey(t *testing.T) {
	db, a, _ := newDB(t)
	s := NewStore(db)
	ctx := context.Background()
	k, err := sshkeys.NewStore(db).Create(ctx, "ci")
	if err != nil {
		t.Fatal(err)
	}
	base := Source{ComposeID: a, Kind: KindGit, RepoURL: "git@github.com:o/r.git", Branch: "main"}
	base.SSHKeyID = &k.ID
	if err := s.Save(ctx, base); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if got.SSHKeyID == nil || *got.SSHKeyID != k.ID || got.PrivateKey != k.PrivateKey || got.PublicKey != k.PublicKey || got.SSHKeyName != "ci" {
		t.Fatalf("joined key = %+v", got)
	}
	base.SSHKeyID = nil
	if err := s.Save(ctx, base); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, a)
	if got.SSHKeyID != nil || got.PrivateKey != "" || got.PublicKey != "" || got.SSHKeyName != "" {
		t.Fatalf("without key = %+v", got)
	}
}

func TestSourceCascadesWithCompose(t *testing.T) {
	db, a, _ := newDB(t)
	s := NewStore(db)
	ctx := context.Background()
	if err := s.Save(ctx, Source{ComposeID: a, Kind: KindZip, ZipName: "x.zip", ZipSize: 5, ZipFiles: 2}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM composes WHERE id = ?`, a); err != nil {
		t.Fatalf("delete compose: %v", err)
	}
	if _, err := s.Get(ctx, a); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get after compose delete err = %v, want sql.ErrNoRows", err)
	}
}
