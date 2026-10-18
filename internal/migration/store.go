// Package migration moves everything one panel manages to an empty panel on
// another host (ADR-0018). The old host is the Source: it issues a Migration
// token, holds the Migration lock and streams items. The new host is the
// Target: it claims the token, pulls every item and finalizes.
package migration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Step is how far the old host's Migration lock has got. Each step is
// persisted before it acts, so a restart redoes it.
type Step string

const (
	StepNone        Step = ""
	StepLocked      Step = "locked"      // lock persisted, queue canceled
	StepStopping    Step = "stopping"    // waiting for the running Deployment, then stopping Composes
	StepMaintenance Step = "maintenance" // maintenance container started
	StepReady       Step = "ready"       // manifest built, items may stream
	StepMigrated    Step = "migrated"
)

// LockState is everything the lock needs to survive a restart.
type LockState struct {
	RunningComposeIDs []int64   `json:"runningComposeIds"`
	HostPaths         []string  `json:"hostPaths"`
	Manifest          *Manifest `json:"manifest,omitempty"`
	ClaimedBy         string    `json:"claimedBy"`
	TargetURL         string    `json:"targetUrl"`
	LockedAt          int64     `json:"lockedAt"`
	MigratedAt        int64     `json:"migratedAt,omitempty"`
}

var (
	ErrBadLifetime    = errors.New("lifetime must be 1h, 24h or 168h")
	ErrInvalidToken   = errors.New("invalid or expired migration token")
	ErrAlreadyClaimed = errors.New("this migration token was already claimed")
	ErrClaimLocked    = errors.New("too many wrong migration tokens, try again in a minute")
	ErrInvalidSession = errors.New("invalid migration session")
	ErrStarted        = errors.New("the migration has already started")
)

const (
	maxClaimFailures = 5
	claimLockFor     = time.Minute
)

var lifetimes = map[time.Duration]bool{time.Hour: true, 24 * time.Hour: true, 168 * time.Hour: true}

// Store keeps the single token, the claim counter and the lock row.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

func NewStore(db *sql.DB, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{db: db, now: now}
}

func newSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func sameHash(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// CreateToken replaces any previous token with a new one.
func (s *Store) CreateToken(ctx context.Context, lifetime time.Duration) (string, time.Time, error) {
	if !lifetimes[lifetime] {
		return "", time.Time{}, ErrBadLifetime
	}
	if step, _, err := s.Lock(ctx); err != nil {
		return "", time.Time{}, err
	} else if step != StepNone {
		return "", time.Time{}, ErrStarted
	}
	token, err := newSecret()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := s.now().UTC().Add(lifetime).Truncate(time.Second)
	_, err = s.db.ExecContext(ctx, `INSERT INTO migration_token (id, token_hash, expires_at) VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET token_hash = excluded.token_hash, expires_at = excluded.expires_at,
		session_hash = NULL, claimed_by = NULL, claimed_at = NULL`, hashSecret(token), expires.Unix())
	if err != nil {
		return "", time.Time{}, fmt.Errorf("store migration token: %w", err)
	}
	return token, expires, nil
}

// RevokeToken deletes the token, which also ends a claimed session. It is
// refused once the Migration has started.
func (s *Store) RevokeToken(ctx context.Context) error {
	if step, _, err := s.Lock(ctx); err != nil {
		return err
	} else if step != StepNone {
		return ErrStarted
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM migration_token")
	return err
}

// Claim spends the token and returns a session key. Five wrong tokens in a
// row refuse every claim for a minute, whatever token is presented.
func (s *Store) Claim(ctx context.Context, token, claimedBy string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	now := s.now().UTC()

	var failures int
	var lockedUntil sql.NullInt64
	err = tx.QueryRowContext(ctx, "SELECT failures, locked_until FROM migration_claim_failures WHERE id = 1").Scan(&failures, &lockedUntil)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if lockedUntil.Valid && now.Unix() < lockedUntil.Int64 {
		return "", ErrClaimLocked
	}

	var tokenHash string
	var expiresAt int64
	var sessionHash sql.NullString
	err = tx.QueryRowContext(ctx, "SELECT token_hash, expires_at, session_hash FROM migration_token WHERE id = 1").Scan(&tokenHash, &expiresAt, &sessionHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err != nil || !sameHash(tokenHash, hashSecret(token)) || now.Unix() >= expiresAt {
		failures++
		var until any
		if failures >= maxClaimFailures {
			failures, until = 0, now.Add(claimLockFor).Unix()
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO migration_claim_failures (id, failures, locked_until) VALUES (1, ?, ?)
			ON CONFLICT(id) DO UPDATE SET failures = excluded.failures, locked_until = excluded.locked_until`, failures, until); err != nil {
			return "", err
		}
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return "", ErrInvalidToken
	}
	if sessionHash.Valid {
		return "", ErrAlreadyClaimed
	}
	key, err := newSecret()
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE migration_token SET session_hash = ?, claimed_by = ?, claimed_at = ? WHERE id = 1", hashSecret(key), claimedBy, now.Unix()); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO migration_claim_failures (id, failures, locked_until) VALUES (1, 0, NULL)
		ON CONFLICT(id) DO UPDATE SET failures = 0, locked_until = NULL`); err != nil {
		return "", err
	}
	return key, tx.Commit()
}

// VerifySession checks a session key presented as a bearer token.
func (s *Store) VerifySession(ctx context.Context, sessionKey string) error {
	var sessionHash sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT session_hash FROM migration_token WHERE id = 1").Scan(&sessionHash)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (!sessionHash.Valid || !sameHash(sessionHash.String, hashSecret(sessionKey)))) {
		return ErrInvalidSession
	}
	return err
}

// TokenStatus reports the live token's expiry, nil when there is none, and
// who claimed it.
func (s *Store) TokenStatus(ctx context.Context) (*time.Time, string, error) {
	var expiresAt int64
	var claimedBy sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT expires_at, claimed_by FROM migration_token WHERE id = 1").Scan(&expiresAt, &claimedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	t := time.Unix(expiresAt, 0).UTC()
	return &t, claimedBy.String, nil
}

// Lock reads the lock row; StepNone means the panel is not locked.
func (s *Store) Lock(ctx context.Context) (Step, LockState, error) {
	var step, state string
	err := s.db.QueryRowContext(ctx, "SELECT step, state FROM migration_lock WHERE id = 1").Scan(&step, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return StepNone, LockState{}, nil
	}
	if err != nil {
		return StepNone, LockState{}, fmt.Errorf("read migration lock: %w", err)
	}
	var st LockState
	if err := json.Unmarshal([]byte(state), &st); err != nil {
		return StepNone, LockState{}, fmt.Errorf("decode migration lock: %w", err)
	}
	return Step(step), st, nil
}

// SetLock persists the lock at step.
func (s *Store) SetLock(ctx context.Context, step Step, st LockState) error {
	state, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO migration_lock (id, step, state, updated_at) VALUES (1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET step = excluded.step, state = excluded.state, updated_at = excluded.updated_at`,
		string(step), string(state), s.now().UTC().Unix())
	if err != nil {
		return fmt.Errorf("persist migration lock: %w", err)
	}
	return nil
}

// ClearAll removes the token, the claim counter and the lock.
func (s *Store) ClearAll(ctx context.Context) error {
	return ClearTables(ctx, s.db)
}

// ClearTables empties the three Migration tables of db.
func ClearTables(ctx context.Context, db *sql.DB) error {
	for _, table := range []string{"migration_lock", "migration_token", "migration_claim_failures"} {
		if _, err := db.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	return nil
}
