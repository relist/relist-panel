package auth

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"

	"golang.org/x/crypto/bcrypt"
)

// AdminEmailEnv and AdminPasswordEnv seed the first admin at boot, so a
// fresh install never has a window in which anyone can claim it.
const (
	AdminEmailEnv    = "RELIST_ADMIN_EMAIL"
	AdminPasswordEnv = "RELIST_ADMIN_PASSWORD"
)

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
