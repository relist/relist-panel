package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"relist-panel/internal/audit"
	"relist-panel/internal/clientip"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// The invite routes are public: the link's token is the credential. An
// expired, used or unknown token, or one whose User is disabled, gets the same
// answer, so a link says nothing about why it stopped working. The token is
// 256 random bits, which is why these routes need no rate limit.

const invalidLink = "This link is no longer valid"

// errLinkGone means the link was used between its lookup and its use.
var errLinkGone = errors.New("link already used")

// markLinkUsed spends link id. It changes nothing and returns errLinkGone when
// the link is already spent, so a link stays single-use however many
// connections the pool holds.
func markLinkUsed(ctx context.Context, tx *sql.Tx, id int64, now time.Time) error {
	result, err := tx.ExecContext(ctx, "UPDATE user_tokens SET used_at = ? WHERE id = ? AND used_at IS NULL", now.UTC().Unix(), id)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return errLinkGone
	}
	return nil
}

type openLink struct {
	id      int64
	userID  int64
	kind    string
	email   string
	isAdmin bool
}

// findOpenLink returns the usable link of token, or sql.ErrNoRows.
func (s *Service) findOpenLink(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, token string) (openLink, error) {
	var link openLink
	err := q.QueryRowContext(ctx, `
		SELECT user_tokens.id, user_tokens.user_id, user_tokens.kind, users.email, users.is_admin OR users.id = ?
		FROM user_tokens
		JOIN users ON users.id = user_tokens.user_id
		WHERE user_tokens.token_hash = ?
		  AND user_tokens.used_at IS NULL
		  AND user_tokens.expires_at > ?
		  AND users.disabled_at IS NULL
	`, OwnerID, hashToken(token), s.now().UTC().Unix()).Scan(&link.id, &link.userID, &link.kind, &link.email, &link.isAdmin)
	return link, err
}

func (s *Service) showInvite(w http.ResponseWriter, r *http.Request) {
	link, err := s.findOpenLink(r.Context(), s.db, r.PathValue("token"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, invalidLink)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"email": link.email, "kind": link.kind})
}

// acceptInvite sets the User's password from the link and signs them in. A
// reset link also turns two-factor off and ends every other session: it is
// how an admin recovers a User who lost their authenticator.
func (s *Service) acceptInvite(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := validatePassword(input.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not secure the password")
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	defer tx.Rollback()
	link, err := s.findOpenLink(r.Context(), tx, r.PathValue("token"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, invalidLink)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}

	if err := markLinkUsed(r.Context(), tx, link.id, s.now()); errors.Is(err, errLinkGone) {
		writeError(w, http.StatusNotFound, invalidLink)
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	now := s.now().UTC().Unix()
	type statement struct {
		query string
		args  []any
	}
	statements := []statement{
		{`UPDATE users SET password_hash = ?, password_from_env = 0, failed_attempts = 0, locked_until = NULL, last_login_at = ?
		  WHERE id = ?`, []any{string(hash), now, link.userID}},
	}
	if link.kind == tokenReset {
		statements = append(statements,
			statement{"UPDATE users SET totp_secret = NULL, totp_pending_secret = NULL, totp_last_step = NULL WHERE id = ?", []any{link.userID}},
			statement{"DELETE FROM sessions WHERE user_id = ?", []any{link.userID}},
		)
	}
	for _, st := range statements {
		if _, err := tx.ExecContext(r.Context(), st.query, st.args...); err != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
	}
	token, expiresAt, err := s.startSession(r.Context(), tx, link.userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create a session")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create a session")
		return
	}
	s.recordAudit(r.Context(), audit.Event{
		Action: "auth.invite_accepted", Outcome: audit.OutcomeOK, ActorUserID: link.userID, Actor: link.email,
		Detail: map[string]any{"kind": string(link.kind)}, IP: clientip.From(r),
	})
	setSessionCookie(w, r, token, expiresAt)
	// isAdmin tells the page where to land: a User who is not an Admin has
	// only their Account to open.
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "email": link.email, "isAdmin": link.isAdmin})
}
