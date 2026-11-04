package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// CreateAdmin creates the Owner, the first User, from email and password, as
// relist-panel create-admin does, and returns the stored (normalized) email.
// With any User already present it returns ErrAdminAlreadyExists.
func CreateAdmin(ctx context.Context, db *sql.DB, email, password string, now func() time.Time) (string, error) {
	normalized, err := validateEmail(email)
	if err != nil {
		return "", err
	}
	if err := validatePassword(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	if err := insertAdmin(ctx, db, normalized, string(hash), now()); err != nil {
		return "", err
	}
	return normalized, nil
}

// AdminExists reports whether the panel has any User yet: until then
// create-admin may make the Owner.
func AdminExists(ctx context.Context, db *sql.DB) (bool, error) {
	var exists int
	err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM users)").Scan(&exists)
	return exists == 1, err
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
	if _, err := tx.ExecContext(r.Context(), "UPDATE users SET password_hash = ?, password_from_env = 0 WHERE id = ?", string(hash), adminID); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	if step.Valid {
		// The code is spent: it cannot be replayed.
		if _, err := tx.ExecContext(r.Context(), "UPDATE users SET totp_last_step = ? WHERE id = ?", step.Int64, adminID); err != nil {
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
