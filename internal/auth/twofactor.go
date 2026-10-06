package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

const (
	// twoFactorPeriod is the TOTP step in seconds. The spec fixes it at 30.
	twoFactorPeriod = 30
	// twoFactorIssuer labels the entry an authenticator app shows.
	twoFactorIssuer = "Relist Panel"
)

// errAuthenticationRequired is returned when a request reaches an
// authenticated route without a live session, for example after another
// request revoked the session.
var errAuthenticationRequired = errors.New("authentication required")

// verifyCode checks a submitted TOTP against secret at time now. It strips
// ASCII spaces first, so an authenticator's "123 456" is accepted, and then
// requires exactly six digits. It accepts the current step and one step of
// skew either side, and returns the matched step only when it is strictly
// after lastStep, so a code can be used once.
func verifyCode(secret, code string, now time.Time, lastStep sql.NullInt64) (int64, bool) {
	code = strings.ReplaceAll(code, " ", "")
	if len(code) != 6 {
		return 0, false
	}
	for _, digit := range code {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}

	current := now.Unix() / twoFactorPeriod
	for _, step := range []int64{current - 1, current, current + 1} {
		expected, err := totp.GenerateCodeCustom(secret, time.Unix(step*twoFactorPeriod, 0), totp.ValidateOpts{
			Period:    twoFactorPeriod,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) != 1 {
			continue
		}
		if lastStep.Valid && step <= lastStep.Int64 {
			return 0, false
		}
		return step, true
	}
	return 0, false
}

// currentSession resolves the request's session cookie to the admin it belongs
// to and returns the token that must be kept when other sessions are revoked.
func (s *Service) currentSession(r *http.Request) (int64, string, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return 0, "", errAuthenticationRequired
	}

	var adminID int64
	err = s.db.QueryRowContext(r.Context(), `
		SELECT admin_id
		FROM sessions
		WHERE token = ? AND expires_at > ?
	`, cookie.Value, s.now().UTC().Unix()).Scan(&adminID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", errAuthenticationRequired
	}
	if err != nil {
		return 0, "", err
	}
	return adminID, cookie.Value, nil
}

// revokeOtherSessions deletes every session of adminID except keepToken. It
// runs in the caller's transaction so the revocation commits with the
// two-factor change it accompanies.
func revokeOtherSessions(ctx context.Context, tx *sql.Tx, adminID int64, keepToken string) error {
	_, err := tx.ExecContext(ctx,
		"DELETE FROM sessions WHERE admin_id = ? AND token <> ?",
		adminID, keepToken,
	)
	return err
}

func (s *Service) account(w http.ResponseWriter, r *http.Request) {
	if err := s.requireDatabase(); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}

	adminID, _, err := s.currentSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var email string
	var secret sql.NullString
	err = s.db.QueryRowContext(r.Context(),
		"SELECT email, totp_secret FROM admin_credentials WHERE id = ?",
		adminID,
	).Scan(&email, &secret)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"email":            email,
		"twoFactorEnabled": secret.Valid,
	})
}

func (s *Service) twoFactorSetup(w http.ResponseWriter, r *http.Request) {
	if err := s.requireDatabase(); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}

	input, err := decodeCredentials(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	adminID, _, err := s.currentSession(r)
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

	if account.totpSecret.Valid {
		writeError(w, http.StatusConflict, "two-factor is already enabled")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(account.passwordHash), []byte(input.Password)); err != nil {
		if err := s.recordFailure(r.Context(), tx, adminID); err != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
		if err := tx.Commit(); err != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	key, err := totp.Generate(totp.GenerateOpts{Issuer: twoFactorIssuer, AccountName: account.email})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create the two-factor secret")
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		"UPDATE admin_credentials SET totp_pending_secret = ? WHERE id = ?",
		key.Secret(), adminID,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	if err := s.recordSuccess(r.Context(), tx, adminID); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"secret":     key.Secret(),
		"otpauthUri": key.URL(),
	})
}

func (s *Service) twoFactorEnable(w http.ResponseWriter, r *http.Request) {
	if err := s.requireDatabase(); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}

	input, err := decodeCredentials(w, r)
	if err != nil {
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

	if !account.totpPendingSecret.Valid {
		writeError(w, http.StatusConflict, "two-factor setup has not been started")
		return
	}

	step, ok := verifyCode(account.totpPendingSecret.String, input.Code, s.now().UTC(), account.totpLastStep)
	if !ok {
		if err := s.recordFailure(r.Context(), tx, adminID); err != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
		if err := tx.Commit(); err != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid code")
		return
	}

	if _, err := tx.ExecContext(r.Context(), `
		UPDATE admin_credentials
		SET totp_secret = ?, totp_pending_secret = NULL, totp_last_step = ?
		WHERE id = ?
	`, account.totpPendingSecret.String, step, adminID); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
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

func (s *Service) twoFactorDisable(w http.ResponseWriter, r *http.Request) {
	if err := s.requireDatabase(); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}

	input, err := decodeCredentials(w, r)
	if err != nil {
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

	if !account.totpSecret.Valid {
		writeError(w, http.StatusConflict, "two-factor is not enabled")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(account.passwordHash), []byte(input.Password)); err != nil {
		if err := s.recordFailure(r.Context(), tx, adminID); err != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
		if err := tx.Commit(); err != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	if _, ok := verifyCode(account.totpSecret.String, input.Code, s.now().UTC(), account.totpLastStep); !ok {
		if err := s.recordFailure(r.Context(), tx, adminID); err != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
		if err := tx.Commit(); err != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid code")
		return
	}

	if _, err := tx.ExecContext(r.Context(), `
		UPDATE admin_credentials
		SET totp_secret = NULL, totp_pending_secret = NULL, totp_last_step = NULL
		WHERE id = ?
	`, adminID); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
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
