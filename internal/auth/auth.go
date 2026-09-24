// Package auth owns the single administrator credential and its sessions.
package auth

import (
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
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type admin struct {
	id           int64
	email        string
	passwordHash string
}

// New constructs the auth service over the application's migrated SQLite
// database.
func New(db *sql.DB) *Service {
	return &Service{db: db, now: time.Now}
}

// RegisterRoutes adds the public authentication endpoints to mux.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/status", s.status)
	mux.HandleFunc("POST /api/auth/setup", s.setup)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
}

// Middleware protects every route except the health endpoint, auth endpoints,
// and the frontend files needed to render the auth screens.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicPath(r.URL.Path) {
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
	account, err := s.findAdmin(r, email)
	if errors.Is(err, sql.ErrNoRows) {
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
	if err := bcrypt.CompareHashAndPassword([]byte(account.passwordHash), []byte(input.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	now := s.now().UTC()
	expiresAt := now.Add(sessionLifetime)
	token, err := newSessionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create a session")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO sessions (token, admin_id, expires_at, created_at)
		VALUES (?, ?, ?, ?)
	`, token, account.id, expiresAt.Unix(), now.Unix()); err != nil {
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
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var id int64
	err = tx.QueryRowContext(r.Context(), "SELECT id FROM admin_credentials LIMIT 1").Scan(&id)
	if err == nil {
		return ErrAdminAlreadyExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO admin_credentials (id, email, password_hash, created_at)
		VALUES (1, ?, ?, ?)
	`, email, passwordHash, s.now().UTC().Unix()); err != nil {
		if isUniqueConstraint(err) {
			return ErrAdminAlreadyExists
		}
		return err
	}
	return tx.Commit()
}

func (s *Service) findAdmin(r *http.Request, email string) (admin, error) {
	var account admin
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, email, password_hash
		FROM admin_credentials
		WHERE email = ?
	`, email).Scan(&account.id, &account.email, &account.passwordHash)
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
	// any non-API path. The API namespace itself stays authenticated.
	return !strings.HasPrefix(path, "/api/")
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
