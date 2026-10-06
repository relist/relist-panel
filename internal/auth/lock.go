package auth

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"
)

const (
	// maxFailedAttempts is how many consecutive wrong credentials lock the
	// account. The failure that reaches it sets the lock and still answers as
	// a normal rejection; the 429 starts with the following request.
	maxFailedAttempts = 5
	// lockDuration is how long an account stays locked once it trips.
	lockDuration = time.Minute
)

// newServiceWithClock constructs a Service with a caller-supplied clock. It is
// unexported so tests in this package can drive the lock across time without
// sleeping.
func newServiceWithClock(db *sql.DB, now func() time.Time) *Service {
	return &Service{db: db, now: now}
}

// lockedFor reports how much longer the account is locked, or zero when it is
// not locked. The caller owns tx so the read joins the same transaction as the
// credential check that follows it.
func (s *Service) lockedFor(ctx context.Context, tx *sql.Tx, adminID int64) (time.Duration, error) {
	var lockedUntil sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		"SELECT locked_until FROM admin_credentials WHERE id = ?",
		adminID,
	).Scan(&lockedUntil); err != nil {
		return 0, err
	}
	if !lockedUntil.Valid {
		return 0, nil
	}
	remaining := time.Unix(lockedUntil.Int64, 0).Sub(s.now().UTC())
	if remaining <= 0 {
		return 0, nil
	}
	return remaining, nil
}

// recordFailure counts one consecutive failure. Reaching maxFailedAttempts
// sets the lock for lockDuration and clears the counter so the next window
// starts fresh.
func (s *Service) recordFailure(ctx context.Context, tx *sql.Tx, adminID int64) error {
	var failed int64
	if err := tx.QueryRowContext(ctx,
		"SELECT failed_attempts FROM admin_credentials WHERE id = ?",
		adminID,
	).Scan(&failed); err != nil {
		return err
	}

	failed++
	if failed >= maxFailedAttempts {
		lockedUntil := s.now().UTC().Add(lockDuration).Unix()
		_, err := tx.ExecContext(ctx,
			"UPDATE admin_credentials SET failed_attempts = 0, locked_until = ? WHERE id = ?",
			lockedUntil, adminID,
		)
		return err
	}

	_, err := tx.ExecContext(ctx,
		"UPDATE admin_credentials SET failed_attempts = ? WHERE id = ?",
		failed, adminID,
	)
	return err
}

// recordSuccess clears the consecutive-failure counter. It does not clear the
// lock: a locked request is refused before this is reached.
func (s *Service) recordSuccess(ctx context.Context, tx *sql.Tx, adminID int64) error {
	_, err := tx.ExecContext(ctx,
		"UPDATE admin_credentials SET failed_attempts = 0 WHERE id = ?",
		adminID,
	)
	return err
}

// writeLocked answers with the rate-limit status and a whole-second
// Retry-After, rounded up and never below one.
func writeLocked(w http.ResponseWriter, remaining time.Duration) {
	seconds := int64((remaining + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", fmt.Sprintf("%d", seconds))
	writeError(w, http.StatusTooManyRequests, fmt.Sprintf("too many attempts, try again in %d seconds", seconds))
}
