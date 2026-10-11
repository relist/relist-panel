// Package sources stores the code Source of a Compose: a Git repository or an
// uploaded zip.
package sources

import (
	"context"
	"database/sql"
	"time"
)

type Kind string

const (
	KindGit Kind = "git"
	KindZip Kind = "zip"
)

type Source struct {
	ComposeID  int64
	Kind       Kind
	RepoURL    string
	Branch     string
	Submodules bool
	PrivateKey string
	PublicKey  string
	HeadSHA    string
	LastTestAt *time.Time
	LastTestOK *bool
	ZipName    string
	ZipSize    int64
	ZipFiles   int
	UploadedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const columns = `compose_id, kind, repo_url, branch, submodules, ssh_private_key, ssh_public_key,
	head_sha, last_test_at, last_test_ok, zip_name, zip_size, zip_files, uploaded_at, created_at, updated_at`

// Get returns the Source of a Compose, or sql.ErrNoRows when it has none.
func (s *Store) Get(ctx context.Context, composeID int64) (Source, error) {
	var (
		src                        Source
		kind                       string
		submodules                 int
		testAt, testOK, uploadedAt sql.NullInt64
		createdAt, updatedAt       int64
	)
	err := s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM compose_sources WHERE compose_id = ?`, composeID).Scan(
		&src.ComposeID, &kind, &src.RepoURL, &src.Branch, &submodules, &src.PrivateKey, &src.PublicKey,
		&src.HeadSHA, &testAt, &testOK, &src.ZipName, &src.ZipSize, &src.ZipFiles, &uploadedAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return Source{}, err
	}
	src.Kind = Kind(kind)
	src.Submodules = submodules != 0
	src.LastTestAt = nullTime(testAt)
	if testOK.Valid {
		ok := testOK.Int64 != 0
		src.LastTestOK = &ok
	}
	src.UploadedAt = nullTime(uploadedAt)
	src.CreatedAt = time.Unix(createdAt, 0)
	src.UpdatedAt = time.Unix(updatedAt, 0)
	return src, nil
}

// Save inserts the Source or replaces the one the Compose already has. The
// original created_at is kept on replace.
func (s *Store) Save(ctx context.Context, src Source) error {
	now := time.Now()
	created := src.CreatedAt
	if created.IsZero() {
		created = now
	}
	updated := src.UpdatedAt
	if updated.IsZero() {
		updated = now
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO compose_sources (`+columns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(compose_id) DO UPDATE SET
	kind = excluded.kind, repo_url = excluded.repo_url, branch = excluded.branch,
	submodules = excluded.submodules, ssh_private_key = excluded.ssh_private_key,
	ssh_public_key = excluded.ssh_public_key, head_sha = excluded.head_sha,
	last_test_at = excluded.last_test_at, last_test_ok = excluded.last_test_ok,
	zip_name = excluded.zip_name, zip_size = excluded.zip_size, zip_files = excluded.zip_files,
	uploaded_at = excluded.uploaded_at, updated_at = excluded.updated_at`,
		src.ComposeID, string(src.Kind), src.RepoURL, src.Branch, boolInt(src.Submodules),
		src.PrivateKey, src.PublicKey, src.HeadSHA, unixPtr(src.LastTestAt), boolPtr(src.LastTestOK),
		src.ZipName, src.ZipSize, src.ZipFiles, unixPtr(src.UploadedAt), created.Unix(), updated.Unix())
	return err
}

func (s *Store) Delete(ctx context.Context, composeID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM compose_sources WHERE compose_id = ?`, composeID)
	return err
}

// RecordTest stores the outcome of a connection test.
func (s *Store) RecordTest(ctx context.Context, composeID int64, ok bool, headSHA string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE compose_sources SET last_test_at = ?, last_test_ok = ?, head_sha = ?, updated_at = ? WHERE compose_id = ?`,
		at.Unix(), boolInt(ok), headSHA, at.Unix(), composeID)
	return err
}

func nullTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0)
	return &t
}

func unixPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

func boolPtr(b *bool) any {
	if b == nil {
		return nil
	}
	return boolInt(*b)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
