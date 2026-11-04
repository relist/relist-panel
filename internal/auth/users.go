package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

// The Users routes are admin-only, which authz enforces: none of them is in
// its route table. The handlers enforce only the Owner's rules.

type userView struct {
	ID               int64   `json:"id"`
	Email            string  `json:"email"`
	IsAdmin          bool    `json:"isAdmin"`
	IsOwner          bool    `json:"isOwner"`
	Status           string  `json:"status"`
	TwoFactorEnabled bool    `json:"twoFactorEnabled"`
	LastLoginAt      *string `json:"lastLoginAt"`
	CreatedAt        string  `json:"createdAt"`
}

type linkView struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
	Kind      string `json:"kind"`
}

const userColumns = "id, email, password_hash IS NULL, is_admin, disabled_at IS NOT NULL, totp_secret IS NOT NULL, last_login_at, created_at"

type rowScanner interface{ Scan(...any) error }

func scanUserView(row rowScanner) (userView, error) {
	var (
		view              userView
		invited, disabled bool
		lastLogin         sql.NullInt64
		createdAt         int64
	)
	if err := row.Scan(&view.ID, &view.Email, &invited, &view.IsAdmin, &disabled, &view.TwoFactorEnabled, &lastLogin, &createdAt); err != nil {
		return userView{}, err
	}
	view.IsOwner = view.ID == OwnerID
	view.IsAdmin = view.IsAdmin || view.IsOwner
	switch {
	case disabled:
		view.Status = "disabled"
	case invited:
		view.Status = "invited"
	default:
		view.Status = "active"
	}
	if lastLogin.Valid {
		at := formatTime(lastLogin.Int64)
		view.LastLoginAt = &at
	}
	view.CreatedAt = formatTime(createdAt)
	return view, nil
}

func formatTime(unix int64) string {
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

func readUserView(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) (userView, error) {
	return scanUserView(q.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE id = ?", id))
}

func (s *Service) listUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), "SELECT "+userColumns+" FROM users ORDER BY id")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list users")
		return
	}
	defer rows.Close()
	users := []userView{}
	for rows.Next() {
		view, err := scanUserView(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not list users")
			return
		}
		users = append(users, view)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not list users")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Service) getUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUserID(w, r)
	if !ok {
		return
	}
	view, err := readUserView(r.Context(), s.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the user")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Service) createUser(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFrom(r.Context())
	var input struct {
		Email   string `json:"email"`
		IsAdmin bool   `json:"isAdmin"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	email, err := validateEmail(input.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create the user")
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(r.Context(),
		"INSERT INTO users (email, is_admin, created_at) VALUES (?, ?, ?)",
		email, input.IsAdmin, now.Unix(),
	)
	if err != nil && isUniqueConstraint(err) {
		writeError(w, http.StatusConflict, "A user with this email already exists")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create the user")
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create the user")
		return
	}
	token, expiresAt, err := issueToken(r.Context(), tx, id, tokenInvite, actor.ID, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create the user")
		return
	}
	view, err := readUserView(r.Context(), tx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create the user")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create the user")
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		User userView `json:"user"`
		linkView
	}{view, linkView{Token: token, ExpiresAt: expiresAt.Format(time.RFC3339), Kind: tokenInvite}})
}

func (s *Service) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUserID(w, r)
	if !ok {
		return
	}
	var input struct {
		IsAdmin  *bool `json:"isAdmin"`
		Disabled *bool `json:"disabled"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if id == OwnerID && ((input.IsAdmin != nil && !*input.IsAdmin) || (input.Disabled != nil && *input.Disabled)) {
		writeError(w, http.StatusForbidden, "The owner cannot be disabled or demoted")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update the user")
		return
	}
	defer tx.Rollback()
	if _, err := readUserView(r.Context(), tx, id); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update the user")
		return
	}
	if input.IsAdmin != nil {
		if _, err := tx.ExecContext(r.Context(), "UPDATE users SET is_admin = ? WHERE id = ?", *input.IsAdmin, id); err != nil {
			writeError(w, http.StatusInternalServerError, "could not update the user")
			return
		}
	}
	if input.Disabled != nil {
		var disabledAt any
		if *input.Disabled {
			disabledAt = s.now().UTC().Unix()
		}
		// COALESCE keeps the first disable's time when disabling again.
		if _, err := tx.ExecContext(r.Context(), "UPDATE users SET disabled_at = CASE WHEN ? IS NULL THEN NULL ELSE COALESCE(disabled_at, ?) END WHERE id = ?", disabledAt, disabledAt, id); err != nil {
			writeError(w, http.StatusInternalServerError, "could not update the user")
			return
		}
		if *input.Disabled {
			if _, err := tx.ExecContext(r.Context(), "DELETE FROM sessions WHERE user_id = ?", id); err != nil {
				writeError(w, http.StatusInternalServerError, "could not update the user")
				return
			}
		}
	}
	view, err := readUserView(r.Context(), tx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update the user")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update the user")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Service) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUserID(w, r)
	if !ok {
		return
	}
	if id == OwnerID {
		writeError(w, http.StatusForbidden, "The owner cannot be deleted")
		return
	}
	result, err := s.db.ExecContext(r.Context(), "DELETE FROM users WHERE id = ?", id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete the user")
		return
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resetUser issues a new link: another invite for a User who never set a
// password, otherwise a reset that also signs them out now.
func (s *Service) resetUser(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFrom(r.Context())
	id, ok := pathUserID(w, r)
	if !ok {
		return
	}
	if id == OwnerID && actor.ID != OwnerID {
		writeError(w, http.StatusForbidden, "Only the owner can reset the owner")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create the link")
		return
	}
	defer tx.Rollback()
	view, err := readUserView(r.Context(), tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create the link")
		return
	}
	kind := tokenReset
	switch view.Status {
	case "disabled":
		writeError(w, http.StatusConflict, "Enable this user before sending a link")
		return
	case "invited":
		kind = tokenInvite
	default:
		if _, err := tx.ExecContext(r.Context(), "DELETE FROM sessions WHERE user_id = ?", id); err != nil {
			writeError(w, http.StatusInternalServerError, "could not create the link")
			return
		}
	}
	token, expiresAt, err := issueToken(r.Context(), tx, id, kind, actor.ID, s.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create the link")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create the link")
		return
	}
	writeJSON(w, http.StatusOK, linkView{Token: token, ExpiresAt: expiresAt.Format(time.RFC3339), Kind: kind})
}

func pathUserID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "user not found")
		return 0, false
	}
	return id, true
}

// decodeJSON reads one JSON object into v, answering 400 itself when it
// cannot.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "request must contain a valid JSON object")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, http.StatusBadRequest, "request must contain one JSON object")
		return false
	}
	return true
}
