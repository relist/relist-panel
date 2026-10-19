// Package auth owns the single administrator credential and its sessions.
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
	// selfAuthenticated names further paths the cookie check skips because
	// their handlers authenticate on their own.
	selfAuthenticated func(path string) bool
	// setupBlocked refuses first-admin setup while a server migration is
	// running into this panel, whose admin comes from the old host.
	setupBlocked func() bool
}

// SetSetupBlocker makes setup answer 409 while blocked reports true.
func (s *Service) SetSetupBlocker(blocked func() bool) { s.setupBlocked = blocked }

// AdminExists reports whether the admin account has been set up.
func (s *Service) AdminExists(r *http.Request) (bool, error) { return s.adminExists(r) }

// Authenticated reports whether r carries a valid admin session.
func (s *Service) Authenticated(r *http.Request) (bool, error) { return s.authenticated(r) }

// AllowSelfAuthenticated lets requests to the paths allow reports true for
// past the cookie check: a server migration's session-key routes and its
// pre-setup pull routes (ADR-0018). Their handlers authenticate themselves.
func (s *Service) AllowSelfAuthenticated(allow func(path string) bool) {
	s.selfAuthenticated = allow
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Code     string `json:"code,omitempty"`
}

type admin struct {
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

// RegisterRoutes adds the public authentication endpoints to mux. The account
// and two-factor routes are not public: Middleware keeps them behind a session.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/status", s.status)
	mux.HandleFunc("POST /api/auth/setup", s.setup)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("GET /api/auth/account", s.account)
	mux.HandleFunc("POST /api/auth/password", s.changePassword)
	mux.HandleFunc("POST /api/auth/2fa/setup", s.twoFactorSetup)
	mux.HandleFunc("POST /api/auth/2fa/enable", s.twoFactorEnable)
	mux.HandleFunc("POST /api/auth/2fa/disable", s.twoFactorDisable)
}

// Middleware protects every route except the health endpoint, auth endpoints,
// and the frontend files needed to render the auth screens.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicPath(r.URL.Path) || (s.selfAuthenticated != nil && s.selfAuthenticated(r.URL.Path)) {
			next.ServeHTTP(w, r)
			return
		}

		authenticated, err := s.authenticated(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
		if authenticated {
			next.ServeHTTP(w, r)
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

	exists, err := s.adminExists(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}

	authenticated := false
	if exists {
		authenticated, err = s.authenticated(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
	}
	if !authenticated {
		clearSessionCookieIfPresent(w, r)
	}

	writeJSON(w, http.StatusOK, map[string]bool{
		"setupRequired": !exists,
		"authenticated": authenticated,
	})
}

func (s *Service) setup(w http.ResponseWriter, r *http.Request) {
	if s.setupBlocked != nil && s.setupBlocked() {
		writeError(w, http.StatusConflict, "A migration is in progress")
		return
	}
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
	if err := validatePassword(input.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not secure the password")
		return
	}

	if err := s.createAdmin(r, email, string(hash)); err != nil {
		if errors.Is(err, ErrAdminAlreadyExists) {
			writeError(w, http.StatusConflict, "admin account already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create the admin account")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"email": email})
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
	// The password check below runs inside this transaction deliberately: it
	// keeps the failure counter, the replay step (totp_last_step) and the
	// session insert atomic across concurrent logins. The pool holds a single
	// connection, so this serializes logins for the duration of the bcrypt
	// comparison; widening the pool means revisiting this hold together with
	// the replay guard's unconditional totp_last_step update.
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	defer tx.Rollback()

	account, err := s.findAdmin(r.Context(), tx, email)
	if errors.Is(err, sql.ErrNoRows) {
		// Release the connection before the fallback read: the pool holds a
		// single connection, and adminExists would block behind this open
		// transaction.
		tx.Rollback()
		exists, existsErr := s.adminExists(r)
		if existsErr != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
		if !exists {
			writeError(w, http.StatusConflict, "admin account setup is required")
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}

	remaining, err := s.lockedFor(r.Context(), tx, account.id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	if remaining > 0 {
		writeLocked(w, remaining)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(account.passwordHash), []byte(input.Password)); err != nil {
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

// completeLogin clears the failure counter, records the last accepted TOTP
// step when a code was used, creates the session and writes the success
// response. Every write shares the caller's transaction, so two requests
// cannot both accept the same code.
func (s *Service) completeLogin(w http.ResponseWriter, r *http.Request, tx *sql.Tx, account admin, lastStep sql.NullInt64) {
	if err := s.recordSuccess(r.Context(), tx, account.id); err != nil {
		writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
		return
	}
	if lastStep.Valid {
		if _, err := tx.ExecContext(r.Context(),
			"UPDATE admin_credentials SET totp_last_step = ? WHERE id = ?",
			lastStep.Int64, account.id,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "authentication is temporarily unavailable")
			return
		}
	}

	now := s.now().UTC()
	expiresAt := now.Add(sessionLifetime)
	token, err := newSessionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create a session")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO sessions (token, admin_id, expires_at, created_at)
		VALUES (?, ?, ?, ?)
	`, token, account.id, expiresAt.Unix(), now.Unix()); err != nil {
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

func (s *Service) authenticated(r *http.Request) (bool, error) {
	if err := s.requireDatabase(); err != nil {
		return false, err
	}

	cookie, err := r.Cookie(SessionCookieName)
	if errors.Is(err, http.ErrNoCookie) {
		return false, nil
	}
	if err != nil {
		return false, nil
	}
	if cookie.Value == "" {
		return false, nil
	}

	var one int
	err = s.db.QueryRowContext(r.Context(), `
		SELECT 1
		FROM sessions
		WHERE token = ? AND expires_at > ?
	`, cookie.Value, s.now().UTC().Unix()).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) adminExists(r *http.Request) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(r.Context(), "SELECT EXISTS (SELECT 1 FROM admin_credentials)").Scan(&exists)
	return exists == 1, err
}

func (s *Service) createAdmin(r *http.Request, email, passwordHash string) error {
	return insertAdmin(r.Context(), s.db, email, passwordHash, s.now())
}

// insertAdmin adds the single admin row, or returns ErrAdminAlreadyExists.
func insertAdmin(ctx context.Context, db *sql.DB, email, passwordHash string, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var id int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM admin_credentials LIMIT 1").Scan(&id)
	if err == nil {
		return ErrAdminAlreadyExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO admin_credentials (id, email, password_hash, created_at)
		VALUES (1, ?, ?, ?)
	`, email, passwordHash, now.UTC().Unix()); err != nil {
		if isUniqueConstraint(err) {
			return ErrAdminAlreadyExists
		}
		return err
	}
	return tx.Commit()
}

func (s *Service) findAdmin(ctx context.Context, tx *sql.Tx, email string) (admin, error) {
	return scanAdmin(tx.QueryRowContext(ctx, `
		SELECT id, email, password_hash, totp_secret, totp_pending_secret, totp_last_step, failed_attempts, locked_until
		FROM admin_credentials
		WHERE email = ?
	`, email))
}

func (s *Service) findAdminByID(ctx context.Context, tx *sql.Tx, id int64) (admin, error) {
	return scanAdmin(tx.QueryRowContext(ctx, `
		SELECT id, email, password_hash, totp_secret, totp_pending_secret, totp_last_step, failed_attempts, locked_until
		FROM admin_credentials
		WHERE id = ?
	`, id))
}

func scanAdmin(row *sql.Row) (admin, error) {
	var account admin
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

func validateEmail(value string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(value))
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

func newSessionToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func publicPath(path string) bool {
	switch path {
	case "/healthz", "/api/auth/status", "/api/auth/setup", "/api/auth/login", "/api/auth/logout":
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
