// Package registry owns the Panel registry: the registry:3 container the
// panel runs, its settings and Push credentials, the files it reads, and
// reconcile (.scratch/private-registry/spec.md, ADR-0033).
package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// How Traefik serves TLS for the registry's hostname.
const (
	TLSLetsEncrypt = "letsencrypt"
	TLSCertificate = "certificate" // an uploaded Certificate, chosen by SNI
)

// ErrDuplicateName is a second Push credential with one name.
var ErrDuplicateName = errors.New("a Push credential with this name already exists")

// Settings is the one registry_settings row. SystemSecret and SystemHash are
// the hidden relist-panel login the panel pulls and lists tags with.
type Settings struct {
	Enabled      bool
	Hostname     string
	TLS          string
	SystemSecret string
	SystemHash   string
	LastError    string
}

// Credential is a Push credential: a named htpasswd login. Its token is never
// stored, only the bcrypt Hash.
type Credential struct {
	ID        int64
	Name      string
	Hash      string
	CreatedBy string
	CreatedAt time.Time
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Settings(ctx context.Context) (Settings, error) {
	var st Settings
	var hostname sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT enabled, hostname, tls, system_secret, system_hash, last_error
		FROM registry_settings WHERE id = 1`).
		Scan(&st.Enabled, &hostname, &st.TLS, &st.SystemSecret, &st.SystemHash, &st.LastError)
	st.Hostname = hostname.String
	return st, err
}

// SaveSettings writes every field; an empty Hostname is stored as NULL.
func (s *Store) SaveSettings(ctx context.Context, st Settings) error {
	var hostname any
	if st.Hostname != "" {
		hostname = st.Hostname
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE registry_settings
		SET enabled = ?, hostname = ?, tls = ?, system_secret = ?, system_hash = ?, last_error = ?
		WHERE id = 1`, st.Enabled, hostname, st.TLS, st.SystemSecret, st.SystemHash, st.LastError)
	return err
}

func (s *Store) SetLastError(ctx context.Context, msg string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE registry_settings SET last_error = ? WHERE id = 1`, msg)
	return err
}

const credentialColumns = `id, name, hash, created_by, created_at`

func scanCredential(row interface{ Scan(...any) error }) (Credential, error) {
	var c Credential
	var created int64
	if err := row.Scan(&c.ID, &c.Name, &c.Hash, &c.CreatedBy, &created); err != nil {
		return Credential{}, err
	}
	c.CreatedAt = time.Unix(created, 0)
	return c, nil
}

// Credentials lists every Push credential, ordered by name.
func (s *Store) Credentials(ctx context.Context) ([]Credential, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+credentialColumns+` FROM registry_credentials ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Credential{}
	for rows.Next() {
		c, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CreateCredential(ctx context.Context, c Credential) (Credential, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO registry_credentials (name, hash, created_by, created_at) VALUES (?, ?, ?, ?)`,
		c.Name, c.Hash, c.CreatedBy, c.CreatedAt.Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return Credential{}, ErrDuplicateName
		}
		return Credential{}, fmt.Errorf("push credential: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Credential{}, err
	}
	return scanCredential(s.db.QueryRowContext(ctx, `SELECT `+credentialColumns+` FROM registry_credentials WHERE id = ?`, id))
}

// DeleteCredential removes and returns one; sql.ErrNoRows when there is none.
func (s *Store) DeleteCredential(ctx context.Context, id int64) (Credential, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Credential{}, err
	}
	defer tx.Rollback()
	c, err := scanCredential(tx.QueryRowContext(ctx, `SELECT `+credentialColumns+` FROM registry_credentials WHERE id = ?`, id))
	if err != nil {
		return Credential{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM registry_credentials WHERE id = ?`, id); err != nil {
		return Credential{}, err
	}
	return c, tx.Commit()
}
