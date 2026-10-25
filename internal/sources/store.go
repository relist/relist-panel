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
	ComposeID     int64
	Kind          Kind
	RepoURL       string
	Branch        string
	Submodules    bool
	SSHKeyID      *int64
	SSHKeyName    string // joined from ssh_keys by Get; never saved
	PrivateKey    string // joined from ssh_keys by Get; never saved
	PublicKey     string // joined from ssh_keys by Get; never saved
	HeadSHA       string
	PinnedSHA     string // execution-only revision; never saved as Source configuration
	TreeDirectory string // private last-built Git tree; never part of Source JSON
	LastTestAt    *time.Time
	LastTestOK    *bool
	ZipName       string
	ZipSize       int64
	ZipFiles      int
	UploadedAt    *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// columns are the compose_sources columns Save writes. The retired
// ssh_private_key and ssh_public_key columns keep their defaults.
const columns = `compose_id, kind, repo_url, branch, submodules, ssh_key_id,
	head_sha, last_test_at, last_test_ok, zip_name, zip_size, zip_files, uploaded_at, created_at, updated_at`

// Get returns the Source of a Compose, or sql.ErrNoRows when it has none.
func (s *Store) Get(ctx context.Context, composeID int64) (Source, error) {
	var (
		src                        Source
		keyID                      sql.NullInt64
		kind                       string
		submodules                 int
		testAt, testOK, uploadedAt sql.NullInt64
		createdAt, updatedAt       int64
	)
	err := s.db.QueryRowContext(ctx, `SELECT cs.compose_id, cs.kind, cs.repo_url, cs.branch, cs.submodules, cs.ssh_key_id,
	COALESCE(k.name, ''), COALESCE(k.private_key, ''), COALESCE(k.public_key, ''),
	cs.head_sha, cs.last_test_at, cs.last_test_ok, cs.zip_name, cs.zip_size, cs.zip_files, cs.uploaded_at, cs.created_at, cs.updated_at,
	COALESCE((SELECT source_dir FROM compose_source_materializations m WHERE m.compose_id = cs.compose_id), '')
FROM compose_sources cs LEFT JOIN ssh_keys k ON k.id = cs.ssh_key_id WHERE cs.compose_id = ?`, composeID).Scan(
		&src.ComposeID, &kind, &src.RepoURL, &src.Branch, &submodules, &keyID,
		&src.SSHKeyName, &src.PrivateKey, &src.PublicKey,
		&src.HeadSHA, &testAt, &testOK, &src.ZipName, &src.ZipSize, &src.ZipFiles, &uploadedAt, &createdAt, &updatedAt, &src.TreeDirectory,
	)
	if err != nil {
		return Source{}, err
	}
	src.Kind = Kind(kind)
	if keyID.Valid {
		src.SSHKeyID = &keyID.Int64
	}
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM compose_source_materializations WHERE compose_id = ? AND (? = 'zip' OR NOT EXISTS (SELECT 1 FROM compose_sources WHERE compose_id = ? AND kind = 'git' AND repo_url = ? AND branch = ? AND submodules = ?))`, src.ComposeID, string(src.Kind), src.ComposeID, src.RepoURL, src.Branch, boolInt(src.Submodules)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO compose_sources (`+columns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(compose_id) DO UPDATE SET
	kind = excluded.kind, repo_url = excluded.repo_url, branch = excluded.branch,
	submodules = excluded.submodules, ssh_key_id = excluded.ssh_key_id, head_sha = excluded.head_sha,
	last_test_at = excluded.last_test_at, last_test_ok = excluded.last_test_ok,
	zip_name = excluded.zip_name, zip_size = excluded.zip_size, zip_files = excluded.zip_files,
	uploaded_at = excluded.uploaded_at, updated_at = excluded.updated_at`,
		src.ComposeID, string(src.Kind), src.RepoURL, src.Branch, boolInt(src.Submodules),
		src.SSHKeyID, src.HeadSHA, unixPtr(src.LastTestAt), boolPtr(src.LastTestOK),
		src.ZipName, src.ZipSize, src.ZipFiles, unixPtr(src.UploadedAt), created.Unix(), updated.Unix())
	if err != nil {
		return err
	}
	return tx.Commit()
}

// RecordMaterialization publishes an immutable tree only if the Source still
// matches the queued input. It never overwrites current Source configuration.
func (s *Store) RecordMaterialization(ctx context.Context, src Source, dir string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO compose_source_materializations (compose_id, source_dir)
SELECT compose_id, ? FROM compose_sources WHERE compose_id = ? AND kind = 'git' AND repo_url = ? AND branch = ? AND submodules = ?
ON CONFLICT(compose_id) DO UPDATE SET source_dir = excluded.source_dir`, dir, src.ComposeID, src.RepoURL, src.Branch, boolInt(src.Submodules))
	return err
}

func (s *Store) Delete(ctx context.Context, composeID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM compose_source_materializations WHERE compose_id = ?`, composeID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM compose_sources WHERE compose_id = ?`, composeID); err != nil {
		return err
	}
	return tx.Commit()
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
