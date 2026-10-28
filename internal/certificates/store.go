package certificates

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

// Certificate is one uploaded certificate and key. SANs, Issuer and NotAfter
// are read from the leaf when it is written.
type Certificate struct {
	ID             int64
	Name           string
	CertificatePEM string
	PrivateKeyPEM  string
	SANs           []string
	Issuer         string
	NotAfter       time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

var ErrNameTaken = errors.New("A certificate with this name already exists")

var namePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// CleanName trims name and checks it.
func CleanName(name string) (string, *FieldError) {
	name = strings.TrimSpace(name)
	if !namePattern.MatchString(name) {
		return "", &FieldError{"name", "name must be 1-64 letters, digits, . _ or -"}
	}
	return name, nil
}

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db, now: time.Now}
}

const selectCertificates = `SELECT id, name, certificate_pem, private_key_pem, sans, issuer, not_after, created_at, updated_at FROM certificates`

type scanner interface {
	Scan(dest ...any) error
}

func scanCertificate(row scanner) (Certificate, error) {
	var c Certificate
	var sans string
	var notAfter, createdAt, updatedAt int64
	if err := row.Scan(&c.ID, &c.Name, &c.CertificatePEM, &c.PrivateKeyPEM, &sans, &c.Issuer, &notAfter, &createdAt, &updatedAt); err != nil {
		return Certificate{}, err
	}
	if err := json.Unmarshal([]byte(sans), &c.SANs); err != nil {
		return Certificate{}, err
	}
	c.NotAfter = time.Unix(notAfter, 0).UTC()
	c.CreatedAt = time.Unix(createdAt, 0)
	c.UpdatedAt = time.Unix(updatedAt, 0)
	return c, nil
}

// List returns every certificate ordered by name.
func (s *Store) List(ctx context.Context) ([]Certificate, error) {
	rows, err := s.db.QueryContext(ctx, selectCertificates+` ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	certs := []Certificate{}
	for rows.Next() {
		c, err := scanCertificate(rows)
		if err != nil {
			return nil, err
		}
		certs = append(certs, c)
	}
	return certs, rows.Err()
}

func (s *Store) get(ctx context.Context, id int64) (Certificate, error) {
	return scanCertificate(s.db.QueryRowContext(ctx, selectCertificates+` WHERE id = ?`, id))
}

// Create stores p under name, which must already be clean.
func (s *Store) Create(ctx context.Context, name string, p Parsed) (Certificate, error) {
	sans, err := json.Marshal(p.SANs)
	if err != nil {
		return Certificate{}, err
	}
	now := s.now().Unix()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO certificates (name, certificate_pem, private_key_pem, sans, issuer, not_after, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		name, p.CertificatePEM, p.PrivateKeyPEM, string(sans), p.Issuer, p.NotAfter.Unix(), now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return Certificate{}, ErrNameTaken
		}
		return Certificate{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Certificate{}, err
	}
	return s.get(ctx, id)
}

// Replace swaps the certificate and key of id, keeping its name, or returns
// sql.ErrNoRows.
func (s *Store) Replace(ctx context.Context, id int64, p Parsed) (Certificate, error) {
	sans, err := json.Marshal(p.SANs)
	if err != nil {
		return Certificate{}, err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE certificates
		SET certificate_pem = ?, private_key_pem = ?, sans = ?, issuer = ?, not_after = ?, updated_at = ?
		WHERE id = ?`,
		p.CertificatePEM, p.PrivateKeyPEM, string(sans), p.Issuer, p.NotAfter.Unix(), s.now().Unix(), id)
	if err != nil {
		return Certificate{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return Certificate{}, err
	} else if n == 0 {
		return Certificate{}, sql.ErrNoRows
	}
	return s.get(ctx, id)
}

// Delete removes id, or returns sql.ErrNoRows.
func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM certificates WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
