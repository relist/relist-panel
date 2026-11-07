// Package registryauth owns External registries: credentials for registries
// the panel does not run, and the Docker config every pull and build gets from
// them (.scratch/private-registry/spec.md, ADR-0034).
package registryauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrDuplicateHost is a second External registry for one host.
var ErrDuplicateHost = errors.New("an External registry for this host already exists")

// ErrBadHost is a host that is not a registry host name.
var ErrBadHost = errors.New("host must be a registry host such as ghcr.io or registry.example.com:5000")

// ErrHostPath is a host with a path after it, such as ghcr.io/owner.
var ErrHostPath = errors.New("host takes no path: enter ghcr.io, not ghcr.io/owner")

// DockerHub is the host Docker Hub's credentials are stored under.
const DockerHub = "docker.io"

// External is the credentials for one registry the panel does not run.
type External struct {
	ID            int64
	Host          string
	Username      string
	Secret        string
	LastTestAt    *time.Time
	LastTestOK    bool
	LastTestError string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

var (
	hostPattern = regexp.MustCompile(`^[a-z0-9.-]+(:[0-9]{1,5})?$`)
	hostLabel   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
)

// validHostPort checks what hostPattern lets through: every dot-separated
// label is a DNS label, and a port is 1 to 65535.
func validHostPort(h string) bool {
	name, port, hasPort := strings.Cut(h, ":")
	if hasPort {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	for _, label := range strings.Split(name, ".") {
		if !hostLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// NormalizeHost lowercases a registry host, strips a scheme and a trailing
// slash, and stores Docker Hub's names as docker.io.
func NormalizeHost(s string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(s))
	h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
	h = strings.TrimSuffix(h, "/")
	if strings.Contains(h, "/") {
		return "", ErrHostPath
	}
	if !hostPattern.MatchString(h) || !validHostPort(h) {
		return "", ErrBadHost
	}
	switch h {
	case "index.docker.io", "registry-1.docker.io":
		h = DockerHub
	}
	return h, nil
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const columns = `id, host, username, secret, last_test_at, last_test_ok, last_test_error, created_at, updated_at`

func scan(row interface{ Scan(...any) error }) (External, error) {
	var e External
	var testAt sql.NullInt64
	var created, updated int64
	if err := row.Scan(&e.ID, &e.Host, &e.Username, &e.Secret, &testAt, &e.LastTestOK, &e.LastTestError, &created, &updated); err != nil {
		return External{}, err
	}
	if testAt.Valid {
		at := time.Unix(testAt.Int64, 0)
		e.LastTestAt = &at
	}
	e.CreatedAt, e.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return e, nil
}

// List is every External registry, ordered by host.
func (s *Store) List(ctx context.Context) ([]External, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM external_registries ORDER BY host`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []External{}
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Get returns sql.ErrNoRows when there is no such External registry.
func (s *Store) Get(ctx context.Context, id int64) (External, error) {
	return scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM external_registries WHERE id = ?`, id))
}

func (s *Store) Create(ctx context.Context, e External, now time.Time) (External, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO external_registries (host, username, secret, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		e.Host, e.Username, e.Secret, now.Unix(), now.Unix())
	if err != nil {
		return External{}, duplicate(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return External{}, err
	}
	return s.Get(ctx, id)
}

// Update changes host, username and, when e.Secret is not empty, the secret.
// A changed login forgets its last Test result: it was not this login's.
func (s *Store) Update(ctx context.Context, e External, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE external_registries
		SET last_test_at = CASE WHEN host <> ?1 OR username <> ?2 OR ?3 <> '' THEN NULL ELSE last_test_at END,
		    last_test_ok = CASE WHEN host <> ?1 OR username <> ?2 OR ?3 <> '' THEN 0 ELSE last_test_ok END,
		    last_test_error = CASE WHEN host <> ?1 OR username <> ?2 OR ?3 <> '' THEN '' ELSE last_test_error END,
		    host = ?1, username = ?2, secret = CASE WHEN ?3 = '' THEN secret ELSE ?3 END, updated_at = ?4
		WHERE id = ?5`, e.Host, e.Username, e.Secret, now.Unix(), e.ID)
	if err != nil {
		return duplicate(err)
	}
	return oneRow(res)
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM external_registries WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return oneRow(res)
}

func (s *Store) SetTestResult(ctx context.Context, id int64, ok bool, msg string, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE external_registries SET last_test_at = ?, last_test_ok = ?, last_test_error = ? WHERE id = ?`,
		at.Unix(), ok, msg, id)
	if err != nil {
		return err
	}
	return oneRow(res)
}

func oneRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func duplicate(err error) error {
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrDuplicateHost
	}
	return fmt.Errorf("external registry: %w", err)
}
