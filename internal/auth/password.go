package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// AdminEmailEnv and AdminPasswordEnv seed the first admin at boot, so a
// fresh install never has a window in which anyone can claim it.
const (
	AdminEmailEnv    = "RELIST_ADMIN_EMAIL"
	AdminPasswordEnv = "RELIST_ADMIN_PASSWORD"
)

// SeedAdmin creates the admin from email and password when there is none
// yet; both empty is no seed. Invalid values are an error naming the
// variable, never its value, so the panel never starts with setup left open
// by mistake.
func SeedAdmin(ctx context.Context, db *sql.DB, email, password string, now func() time.Time) (bool, error) {
	if email == "" && password == "" {
		return false, nil
	}
	if email == "" {
		return false, fmt.Errorf("%s is set but %s is not: set both, or neither", AdminPasswordEnv, AdminEmailEnv)
	}
	if password == "" {
		return false, fmt.Errorf("%s is set but %s is not: set both, or neither", AdminEmailEnv, AdminPasswordEnv)
	}
	normalized, err := validateEmail(email)
	if err != nil {
		return false, fmt.Errorf("%s: %w", AdminEmailEnv, err)
	}
	if err := validatePassword(password); err != nil {
		return false, fmt.Errorf("%s: %w", AdminPasswordEnv, err)
	}
	var exists int
	if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM admin_credentials)").Scan(&exists); err != nil {
		return false, err
	}
	if exists == 1 {
		log.Printf("auth: an admin already exists, so %s and %s are ignored", AdminEmailEnv, AdminPasswordEnv)
		return false, nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, err
	}
	if err := insertAdmin(ctx, db, normalized, string(hash), now()); err != nil {
		if errors.Is(err, ErrAdminAlreadyExists) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// passwordFromEnv reports whether hash is still the password the env seeded.
func passwordFromEnv(hash string) bool {
	seeded := os.Getenv(AdminPasswordEnv)
	return seeded != "" && bcrypt.CompareHashAndPassword([]byte(hash), []byte(seeded)) == nil
}

type passwordChange struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
	Code            string `json:"code,omitempty"`
}

// changePassword re-checks the current password, and the code when two-factor
// is on, then replaces the hash and ends every other session.
func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) {
	if err := s.requireDatabase(); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	var input passwordChange
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBody)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := validatePassword(input.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	adminID, token, err := s.currentSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	defer tx.Rollback()
	account, err := s.findAdminByID(r.Context(), tx, adminID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	remaining, err := s.lockedFor(r.Context(), tx, adminID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	if remaining > 0 {
		writeLocked(w, remaining)
		return
	}

	refuse := func(message string) {
		if err := s.recordFailure(r.Context(), tx, adminID); err != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
		if err := tx.Commit(); err != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
		writeError(w, http.StatusUnauthorized, message)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(account.passwordHash), []byte(input.CurrentPassword)); err != nil {
		refuse("the current password is wrong")
		return
	}
	var step sql.NullInt64
	if account.totpSecret.Valid {
		matched, ok := verifyCode(account.totpSecret.String, input.Code, s.now().UTC(), account.totpLastStep)
		if !ok {
			refuse("invalid code")
			return
		}
		step = sql.NullInt64{Int64: matched, Valid: true}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not secure the password")
		return
	}
	if _, err := tx.ExecContext(r.Context(), "UPDATE admin_credentials SET password_hash = ? WHERE id = ?", string(hash), adminID); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	if step.Valid {
		// The code is spent: it cannot be replayed.
		if _, err := tx.ExecContext(r.Context(), "UPDATE admin_credentials SET totp_last_step = ? WHERE id = ?", step.Int64, adminID); err != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
	}
	if err := s.recordSuccess(r.Context(), tx, adminID); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	if err := revokeOtherSessions(r.Context(), tx, adminID, token); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
