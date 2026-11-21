package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// HashSessionToken is how a session's cookie token is stored and looked up:
// the sessions table never holds the token itself.
func HashSessionToken(token string) string {
	return hashToken(token)
}

// startSession creates a session for userID in the caller's transaction. The
// caller sets the cookie once the transaction commits.
func (s *Service) startSession(ctx context.Context, tx *sql.Tx, userID int64) (string, time.Time, error) {
	now := s.now().UTC()
	expiresAt := now.Add(sessionLifetime)
	token, err := newSessionToken()
	if err != nil {
		return "", time.Time{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at, created_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?)
	`, HashSessionToken(token), userID, expiresAt.Unix(), now.Unix(), now.Unix()); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func newSessionToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(sessionLifetime / time.Second),
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookieIfPresent(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(SessionCookieName); err == nil {
		clearSessionCookie(w, r)
	}
}

// currentSession resolves the request's session cookie to the User it belongs
// to and returns the token hash that must be kept when other sessions are
// revoked.
func (s *Service) currentSession(r *http.Request) (int64, string, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return 0, "", errAuthenticationRequired
	}

	var adminID int64
	err = s.db.QueryRowContext(r.Context(), `
		SELECT user_id
		FROM sessions
		WHERE token_hash = ? AND expires_at > ?
	`, HashSessionToken(cookie.Value), s.now().UTC().Unix()).Scan(&adminID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", errAuthenticationRequired
	}
	if err != nil {
		return 0, "", err
	}
	return adminID, HashSessionToken(cookie.Value), nil
}

// revokeOtherSessions deletes every session of adminID except keepHash. It
// runs in the caller's transaction so the revocation commits with the
// two-factor change it accompanies.
func revokeOtherSessions(ctx context.Context, tx *sql.Tx, adminID int64, keepHash string) error {
	_, err := tx.ExecContext(ctx,
		"DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?",
		adminID, keepHash,
	)
	return err
}
