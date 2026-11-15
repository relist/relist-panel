// Package sshkeys stores the host-wide library of SSH keys that Git Sources
// pick from to fetch private repositories.
package sshkeys

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Key struct {
	ID         int64
	Name       string
	PrivateKey string
	PublicKey  string
	CreatedAt  time.Time
	UsedBy     []Use
	// UsedByDestinations are the names of the Backup destinations that log in
	// with the key, sorted.
	UsedByDestinations []string
}

// Use is a Git Source that has picked a key.
type Use struct {
	ProjectID     int64
	ProjectName   string
	EnvironmentID int64
	ComposeID     int64
	ComposeName   string
}

var (
	ErrNameTaken    = errors.New("An SSH key with this name already exists")
	ErrNameRequired = errors.New("name is required")
	ErrNameInvalid  = errors.New("name must be at most 100 characters with no control characters")
)

// ErrKeyInUse is a key a Backup destination logs in with; it cannot be
// deleted. (A Git Source's key can: the Source loses it.)
type ErrKeyInUse struct{ Destinations []string }

func (e ErrKeyInUse) Error() string {
	return "used by Backup destinations: " + strings.Join(e.Destinations, ", ")
}

type Store struct {
	db *sql.DB
}

const maxNameRunes = 100

// cleanName trims name and checks it is usable.
func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrNameRequired
	}
	if utf8.RuneCountInString(name) > maxNameRunes || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", ErrNameInvalid
	}
	return name, nil
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const selectKeys = `SELECT id, name, private_key, public_key, created_at FROM ssh_keys`

type scanner interface {
	Scan(dest ...any) error
}

func scanKey(row scanner) (Key, error) {
	var k Key
	var createdAt int64
	if err := row.Scan(&k.ID, &k.Name, &k.PrivateKey, &k.PublicKey, &createdAt); err != nil {
		return Key{}, err
	}
	k.CreatedAt = time.Unix(createdAt, 0)
	k.UsedBy = []Use{}
	k.UsedByDestinations = []string{}
	return k, nil
}

// List returns every key ordered by name, each with the Sources using it.
func (s *Store) List(ctx context.Context) ([]Key, error) {
	rows, err := s.db.QueryContext(ctx, selectKeys+` ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []Key{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	uses, err := s.uses(ctx, 0)
	if err != nil {
		return nil, err
	}
	dests, err := destinationUses(ctx, s.db, 0)
	if err != nil {
		return nil, err
	}
	for i := range keys {
		keys[i].UsedBy = append(keys[i].UsedBy, uses[keys[i].ID]...)
		keys[i].UsedByDestinations = append(keys[i].UsedByDestinations, dests[keys[i].ID]...)
	}
	return keys, nil
}

// Get returns one key, or sql.ErrNoRows.
func (s *Store) Get(ctx context.Context, id int64) (Key, error) {
	k, err := scanKey(s.db.QueryRowContext(ctx, selectKeys+` WHERE id = ?`, id))
	if err != nil {
		return Key{}, err
	}
	uses, err := s.uses(ctx, id)
	if err != nil {
		return Key{}, err
	}
	k.UsedBy = append(k.UsedBy, uses[id]...)
	dests, err := destinationUses(ctx, s.db, id)
	if err != nil {
		return Key{}, err
	}
	k.UsedByDestinations = append(k.UsedByDestinations, dests[id]...)
	return k, nil
}

// Create generates an ed25519 key named name (trimmed).
func (s *Store) Create(ctx context.Context, name string) (Key, error) {
	name, err := cleanName(name)
	if err != nil {
		return Key{}, err
	}
	priv, pub, err := GenerateKey("relist-" + name)
	if err != nil {
		return Key{}, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO ssh_keys (name, private_key, public_key, created_at) VALUES (?, ?, ?, ?)`,
		name, priv, pub, time.Now().Unix())
	if err != nil {
		if isUniqueConstraint(err) {
			return Key{}, ErrNameTaken
		}
		return Key{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Key{}, err
	}
	return s.Get(ctx, id)
}

// Rename changes a key's name; the key material is untouched.
func (s *Store) Rename(ctx context.Context, id int64, name string) (Key, error) {
	name, err := cleanName(name)
	if err != nil {
		return Key{}, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE ssh_keys SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		if isUniqueConstraint(err) {
			return Key{}, ErrNameTaken
		}
		return Key{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return Key{}, err
	} else if n == 0 {
		return Key{}, sql.ErrNoRows
	}
	return s.Get(ctx, id)
}

// Delete removes a key, unless a Backup destination uses it (ErrKeyInUse).
// The Sources using it keep no key, and their last test result, which used
// that key, is cleared.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	dests, err := destinationUses(ctx, tx, id)
	if err != nil {
		return err
	}
	if names := dests[id]; len(names) > 0 {
		return ErrKeyInUse{names}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE compose_sources SET last_test_at = NULL, last_test_ok = NULL WHERE ssh_key_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM ssh_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

// uses maps key id to its Sources, ordered by project then compose name. A
// zero keyID means every key.
func (s *Store) uses(ctx context.Context, keyID int64) (map[int64][]Use, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.ssh_key_id, p.id, p.name, e.id, c.id, c.name
		FROM compose_sources s
		JOIN composes c ON c.id = s.compose_id
		JOIN environments e ON e.id = c.environment_id
		JOIN projects p ON p.id = e.project_id
		WHERE s.ssh_key_id IS NOT NULL AND (? = 0 OR s.ssh_key_id = ?)
		ORDER BY p.name, c.name, c.id`, keyID, keyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	uses := map[int64][]Use{}
	for rows.Next() {
		var id int64
		var u Use
		if err := rows.Scan(&id, &u.ProjectID, &u.ProjectName, &u.EnvironmentID, &u.ComposeID, &u.ComposeName); err != nil {
			return nil, err
		}
		uses[id] = append(uses[id], u)
	}
	return uses, rows.Err()
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// destinationUses maps key id to the names of the Backup destinations using
// it, sorted. A zero keyID means every key.
func destinationUses(ctx context.Context, q queryer, keyID int64) (map[int64][]string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT ssh_key_id, name FROM backup_destinations
		WHERE ssh_key_id IS NOT NULL AND (? = 0 OR ssh_key_id = ?)
		ORDER BY name`, keyID, keyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = append(out[id], name)
	}
	return out, rows.Err()
}

func isUniqueConstraint(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
