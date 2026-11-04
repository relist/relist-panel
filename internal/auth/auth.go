// Package auth owns the panel's users, their credentials and sessions.
package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const (
	SessionCookieName = "relist_session"
	sessionLifetime   = 30 * 24 * time.Hour
	maxRequestBody    = 1 << 20
)

var (
	ErrAdminAlreadyExists  = errors.New("admin account already exists")
	errDatabaseUnavailable = errors.New("auth database is unavailable")
)

type Service struct {
	db  *sql.DB
	now func() time.Time
	// comparePassword checks a password against its bcrypt hash; tests swap it
	// to hold a login inside the comparison.
	comparePassword func(hash, password []byte) error
	// loginMu serializes whole login attempts (see login).
	loginMu sync.Mutex
	// selfAuthenticated names further paths the cookie check skips because
	// their handlers authenticate on their own.
	selfAuthenticated func(path string) bool
	// version is the build's version, shown by the public status route.
	version string
}

// AllowSelfAuthenticated lets requests to the paths allow reports true for
// past the cookie check: a server migration's session-key routes
// (ADR-0018). Their handlers authenticate themselves.
func (s *Service) AllowSelfAuthenticated(allow func(path string) bool) {
	s.selfAuthenticated = allow
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Code     string `json:"code,omitempty"`
}

type userRow struct {
	id                int64
	email             string
	passwordHash      string
	totpSecret        sql.NullString
	totpPendingSecret sql.NullString
	totpLastStep      sql.NullInt64
	failedAttempts    int64
	lockedUntil       sql.NullInt64
}

// New constructs the auth service over the application's migrated SQLite
// database.
func New(db *sql.DB) *Service {
	return newServiceWithClock(db, time.Now)
}

// WithVersion sets the build version that GET /api/auth/status reports.
func (s *Service) WithVersion(version string) *Service {
	s.version = version
	return s
}

// RegisterRoutes adds the public authentication endpoints to mux. The account
// and two-factor routes are not public: Middleware keeps them behind a session.
// The Users routes are admin-only, which authz enforces.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/status", s.status)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("GET /api/auth/account", s.account)
	mux.HandleFunc("POST /api/auth/password", s.changePassword)
	mux.HandleFunc("POST /api/auth/2fa/setup", s.twoFactorSetup)
	mux.HandleFunc("POST /api/auth/2fa/enable", s.twoFactorEnable)
	mux.HandleFunc("POST /api/auth/2fa/disable", s.twoFactorDisable)
	mux.HandleFunc("GET /api/users", s.listUsers)
	mux.HandleFunc("POST /api/users", s.createUser)
	mux.HandleFunc("GET /api/users/{id}", s.getUser)
	mux.HandleFunc("PATCH /api/users/{id}", s.updateUser)
	mux.HandleFunc("DELETE /api/users/{id}", s.deleteUser)
	mux.HandleFunc("POST /api/users/{id}/reset", s.resetUser)
	mux.HandleFunc("GET /api/invites/{token}", s.showInvite)
	mux.HandleFunc("POST /api/invites/{token}", s.acceptInvite)
}

// Middleware protects every route except the health endpoint, auth endpoints,
// and the frontend files needed to render the auth screens.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicPath(r.URL.Path) || (s.selfAuthenticated != nil && s.selfAuthenticated(r.URL.Path)) {
			next.ServeHTTP(w, r)
			return
		}

		user, authenticated, err := s.authenticated(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
		if authenticated {
			next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
			return
		}

		clearSessionCookie(w, r)
		writeError(w, http.StatusUnauthorized, "authentication required")
	})
}

func (s *Service) status(w http.ResponseWriter, r *http.Request) {
	if err := s.requireDatabase(); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}

	exists, err := AdminExists(r.Context(), s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}

	authenticated := false
	var user User
	if exists {
		user, authenticated, err = s.authenticated(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
	}
	if !authenticated {
		clearSessionCookieIfPresent(w, r)
	}

	body := map[string]any{"authenticated": authenticated, "version": s.version}
	if authenticated {
		body["user"] = map[string]any{"id": user.ID, "email": user.Email, "isAdmin": user.IsAdmin, "isOwner": user.IsOwner()}
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	if err := s.requireDatabase(); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}

	input, err := decodeCredentials(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	email, err := validateEmail(input.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// One admin signs in, so a process-wide mutex serializes whole login
	// attempts. It replaces the single database connection as the serializer
	// the failure counter, the replay step (totp_last_step) and the session
	// insert depend on: parallel wrong guesses still run strictly one after
	// another, and each sees the failure the previous one recorded. That lets
	// the bcrypt comparison run outside any transaction, so it no longer
	// holds the pool's only connection while every other request waits. The
	// attempt reads the account and the lockout first, compares the password
	// with no connection held, then re-reads the account in the write
	// transaction and refuses if the hash changed or the lock started
	// meanwhile (a password change bypasses this mutex). Widening the pool
	// does not change this; the mutex alone provides the ordering.
	s.loginMu.Lock()
	defer s.loginMu.Unlock()

	account, remaining, err := s.readLoginAccount(r.Context(), email)
	if errors.Is(err, sql.ErrNoRows) {
		// No User who can sign in has this email. Spend a bcrypt comparison
		// anyway, so the answer takes as long as a wrong password and its
		// timing does not tell which emails belong to active Users.
		_ = s.comparePassword(dummyHash(), []byte(input.Password))
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	if remaining > 0 {
		writeLocked(w, remaining)
		return
	}

	passwordErr := s.comparePassword([]byte(account.passwordHash), []byte(input.Password))

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	defer tx.Rollback()

	staleHash := account.passwordHash
	account, err = s.findAdminByID(r.Context(), tx, account.id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	remaining, err = s.lockedFor(r.Context(), tx, account.id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	if remaining > 0 {
		writeLocked(w, remaining)
		return
	}
	if account.passwordHash != staleHash {
		// The password changed while it was being checked: the verdict was
		// for the old one, so it counts neither for nor against the account.
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	if passwordErr != nil {
		if err := s.recordFailure(r.Context(), tx, account.id); err != nil {
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

	if account.totpSecret.Valid {
		if input.Code == "" {
			// The normal first step of a two-factor sign-in. It is not a
			// failure, so nothing is written and the counter is untouched.
			tx.Rollback()
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":        "two-factor code required",
				"codeRequired": true,
			})
			return
		}

		step, ok := verifyCode(account.totpSecret.String, input.Code, s.now().UTC(), account.totpLastStep)
		if !ok {
			if err := s.recordFailure(r.Context(), tx, account.id); err != nil {
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

		s.completeLogin(w, r, tx, account, sql.NullInt64{Int64: step, Valid: true})
		return
	}

	// Two-factor is off: any code sent is ignored.
	s.completeLogin(w, r, tx, account, sql.NullInt64{})
}

// readLoginAccount is the login's short first phase: it finds the account and
// reads how long it stays locked, then releases the connection.
func (s *Service) readLoginAccount(ctx context.Context, email string) (userRow, time.Duration, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return userRow{}, 0, err
	}
	defer tx.Rollback()

	account, err := s.findAdmin(ctx, tx, email)
	if err != nil {
		return userRow{}, 0, err
	}
	remaining, err := s.lockedFor(ctx, tx, account.id)
	if err != nil {
		return userRow{}, 0, err
	}
	return account, remaining, nil
}

// completeLogin clears the failure counter, records the last accepted TOTP
// step when a code was used, creates the session and writes the success
// response. Every write shares the caller's transaction, so two requests
// cannot both accept the same code.
func (s *Service) completeLogin(w http.ResponseWriter, r *http.Request, tx *sql.Tx, account userRow, lastStep sql.NullInt64) {
	if err := s.recordSuccess(r.Context(), tx, account.id); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	if _, err := tx.ExecContext(r.Context(), "UPDATE users SET last_login_at = ? WHERE id = ?", s.now().UTC().Unix(), account.id); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	if lastStep.Valid {
		if _, err := tx.ExecContext(r.Context(),
			"UPDATE users SET totp_last_step = ? WHERE id = ?",
			lastStep.Int64, account.id,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
	}

	token, expiresAt, err := s.startSession(r.Context(), tx, account.id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create a session")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create a session")
		return
	}

	setSessionCookie(w, r, token, expiresAt)
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"email":         account.email,
	})
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
		INSERT INTO sessions (token, user_id, expires_at, created_at)
		VALUES (?, ?, ?, ?)
	`, token, userID, expiresAt.Unix(), now.Unix()); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.requireDatabase(); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}

	if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
		if _, err := s.db.ExecContext(r.Context(), "DELETE FROM sessions WHERE token = ?", cookie.Value); err != nil {
			writeError(w, http.StatusInternalServerError, "could not end the session")
			return
		}
	}

	clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// authenticated resolves the request's session cookie to its User. A
// disabled User's sessions count as signed out, so a disable takes effect on
// the next request even before its sessions are deleted.
func (s *Service) authenticated(r *http.Request) (User, bool, error) {
	if err := s.requireDatabase(); err != nil {
		return User{}, false, err
	}

	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return User{}, false, nil
	}

	var user User
	var isAdmin bool
	err = s.db.QueryRowContext(r.Context(), `
		SELECT users.id, users.email, users.is_admin
		FROM sessions
		JOIN users ON users.id = sessions.user_id
		WHERE sessions.token = ? AND sessions.expires_at > ? AND users.disabled_at IS NULL
	`, cookie.Value, s.now().UTC().Unix()).Scan(&user.ID, &user.Email, &isAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	user.IsAdmin = isAdmin || user.ID == OwnerID
	return user, true, nil
}

// insertAdmin adds the Owner (id 1) as the first User, or returns
// ErrAdminAlreadyExists once any User exists.
func insertAdmin(ctx context.Context, db *sql.DB, email, passwordHash string, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var id int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM users LIMIT 1").Scan(&id)
	if err == nil {
		return ErrAdminAlreadyExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (id, email, password_hash, is_admin, created_at)
		VALUES (1, ?, ?, 1, ?)
	`, email, passwordHash, now.UTC().Unix()); err != nil {
		if isUniqueConstraint(err) {
			return ErrAdminAlreadyExists
		}
		return err
	}
	return tx.Commit()
}

func (s *Service) findAdmin(ctx context.Context, tx *sql.Tx, email string) (userRow, error) {
	return scanAdmin(tx.QueryRowContext(ctx, `
		SELECT id, email, password_hash, totp_secret, totp_pending_secret, totp_last_step, failed_attempts, locked_until
		FROM users
		WHERE email = ? AND password_hash IS NOT NULL AND disabled_at IS NULL
	`, email))
}

func (s *Service) findAdminByID(ctx context.Context, tx *sql.Tx, id int64) (userRow, error) {
	return scanAdmin(tx.QueryRowContext(ctx, `
		SELECT id, email, password_hash, totp_secret, totp_pending_secret, totp_last_step, failed_attempts, locked_until
		FROM users
		WHERE id = ? AND password_hash IS NOT NULL AND disabled_at IS NULL
	`, id))
}

func scanAdmin(row *sql.Row) (userRow, error) {
	var account userRow
	err := row.Scan(
		&account.id,
		&account.email,
		&account.passwordHash,
		&account.totpSecret,
		&account.totpPendingSecret,
		&account.totpLastStep,
		&account.failedAttempts,
		&account.lockedUntil,
	)
	return account, err
}

var (
	dummyHashOnce  sync.Once
	dummyHashValue []byte
)

// dummyHash is a bcrypt hash at the cost real passwords use, made once, for
// a sign-in whose email matches no User that can sign in.
func dummyHash() []byte {
	dummyHashOnce.Do(func() {
		hash, err := bcrypt.GenerateFromPassword([]byte("relist-panel dummy password"), bcrypt.DefaultCost)
		if err == nil {
			dummyHashValue = hash
		}
	})
	return dummyHashValue
}

func (s *Service) requireDatabase() error {
	if s == nil || s.db == nil {
		return errDatabaseUnavailable
	}
	return nil
}

func decodeCredentials(w http.ResponseWriter, r *http.Request) (credentials, error) {
	var input credentials
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return credentials{}, errors.New("request must contain a valid JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return credentials{}, errors.New("request must contain one JSON object")
	}
	return input, nil
}

// NormalizeEmail is how an admin email is stored, so a lookup by another
// spelling of it finds the same admin.
func NormalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validateEmail(value string) (string, error) {
	email := NormalizeEmail(value)
	if email == "" || utf8.RuneCountInString(email) > 254 {
		return "", errors.New("email is required and must be valid")
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return "", errors.New("email is required and must be valid")
	}
	return email, nil
}

func validatePassword(value string) error {
	if utf8.RuneCountInString(value) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	if len([]byte(value)) > 72 {
		return errors.New("password must be 72 bytes or fewer")
	}
	return nil
}

// GeneratePassword returns a random 24-character password for create-admin
// --generate: 18 bytes from crypto/rand, base64url without padding.
func GeneratePassword() (string, error) {
	bytes := make([]byte, 18)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func newSessionToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func publicPath(path string) bool {
	switch path {
	case "/healthz", "/api/auth/status", "/api/auth/login", "/api/auth/logout":
		return true
	}
	// An invite or reset link is how a User without a password gets one.
	if strings.HasPrefix(path, "/api/invites/") {
		return true
	}

	// The HTML shell is public wherever it is served: the frontend routes with
	// history URLs (`/projects/1/env/1/compose/1`), so the shell answers under
	// any non-API path. The API namespace itself stays authenticated — `/api`
	// included, so the bare form cannot hand back the public shell.
	return path != "/api" && !strings.HasPrefix(path, "/api/")
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

func requestIsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	forwardedProto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	return strings.EqualFold(forwardedProto, "https")
}

func isUniqueConstraint(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
