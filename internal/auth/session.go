package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
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
	expiresAt := now.Add(sessionMaxLifetime)
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
		MaxAge:   int(sessionMaxLifetime / time.Second),
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

// liveSessionRow is a live session and the enabled User it belongs to.
type liveSessionRow struct {
	hash     string
	user     User
	lastSeen time.Time
}

// liveSession resolves a cookie token to its session when that session is
// live: under the 7-day cap, used within the idle timeout, and owned by an
// enabled User. A live session not touched for sessionTouchInterval gets
// last_seen_at = now; a failed touch is logged and the session stays live.
//
// It writes through s.db, and the panel has one database connection, so it
// must never run while the caller holds an open transaction.
func (s *Service) liveSession(ctx context.Context, token string) (liveSessionRow, bool, error) {
	now := s.now().UTC()
	session := liveSessionRow{hash: HashSessionToken(token)}
	var isAdmin bool
	var lastSeen int64
	err := s.db.QueryRowContext(ctx, `
		SELECT users.id, users.email, users.is_admin, sessions.last_seen_at
		FROM sessions
		JOIN users ON users.id = sessions.user_id
		WHERE sessions.token_hash = ? AND sessions.expires_at > ? AND sessions.last_seen_at > ?
			AND users.disabled_at IS NULL
	`, session.hash, now.Unix(), now.Add(-sessionIdleTimeout).Unix()).Scan(&session.user.ID, &session.user.Email, &isAdmin, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return liveSessionRow{}, false, nil
	}
	if err != nil {
		return liveSessionRow{}, false, err
	}
	session.user.IsAdmin = isAdmin || session.user.ID == OwnerID
	session.lastSeen = time.Unix(lastSeen, 0).UTC()

	if now.Sub(session.lastSeen) >= sessionTouchInterval {
		if _, err := s.db.ExecContext(ctx, "UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?", now.Unix(), session.hash); err != nil {
			log.Printf("auth: touch session: %v", err)
		} else {
			session.lastSeen = now
		}
	}
	return session, true, nil
}

// currentSession resolves the request's session cookie to the User it belongs
// to and returns the token hash that must be kept when other sessions are
// revoked. Like liveSession, it must run before the caller opens a
// transaction.
func (s *Service) currentSession(r *http.Request) (int64, string, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return 0, "", errAuthenticationRequired
	}
	session, live, err := s.liveSession(r.Context(), cookie.Value)
	if err != nil {
		return 0, "", err
	}
	if !live {
		return 0, "", errAuthenticationRequired
	}
	return session.user.ID, session.hash, nil
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

// PruneSessions deletes every dead session, capped or idle, and answers how
// many it deleted.
func (s *Service) PruneSessions(ctx context.Context) (int64, error) {
	now := s.now().UTC()
	result, err := s.db.ExecContext(ctx,
		"DELETE FROM sessions WHERE expires_at <= ? OR last_seen_at <= ?",
		now.Unix(), now.Add(-sessionIdleTimeout).Unix(),
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// RunSessionCleanup prunes dead sessions at once, then every
// sessionCleanupInterval, until ctx is done.
func (s *Service) RunSessionCleanup(ctx context.Context) {
	ticker := time.NewTicker(sessionCleanupInterval)
	defer ticker.Stop()
	for {
		if n, err := s.PruneSessions(ctx); err != nil {
			log.Printf("auth: prune sessions: %v", err)
		} else if n > 0 {
			log.Printf("auth: pruned %d dead sessions", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
