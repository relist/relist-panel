package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"relist-panel/internal/database"
)

func openResetDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// seedResetAdmin inserts the single admin row with two-factor state and a
// future lock. totp_secret drives WasEnabled: NULL means two-factor is off.
func seedResetAdmin(t *testing.T, db *sql.DB, totpSecret sql.NullString) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO admin_credentials
			(id, email, password_hash, created_at, totp_secret, totp_pending_secret, totp_last_step, failed_attempts, locked_until)
		VALUES (1, 'admin@example.com', 'hash', 1, ?, 'PENDING', 42, 3, ?)
	`, totpSecret, time.Now().Add(time.Minute).Unix()); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
}

func seedResetSession(t *testing.T, db *sql.DB, token string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO sessions (token, admin_id, expires_at, created_at)
		VALUES (?, 1, ?, 1)
	`, token, time.Now().Add(24*time.Hour).Unix()); err != nil {
		t.Fatalf("seed session %q: %v", token, err)
	}
}

func TestResetTwoFactorClearsStateAndSessions(t *testing.T) {
	db := openResetDatabase(t)
	seedResetAdmin(t, db, sql.NullString{String: "SECRET", Valid: true})
	seedResetSession(t, db, "session-one")
	seedResetSession(t, db, "session-two")

	result, err := ResetTwoFactor(context.Background(), db, "admin@example.com")
	if err != nil {
		t.Fatalf("ResetTwoFactor() error = %v", err)
	}

	want := ResetResult{Email: "admin@example.com", WasEnabled: true, SessionsRevoked: 2}
	if result != want {
		t.Fatalf("ResetTwoFactor() = %+v, want %+v", result, want)
	}

	var (
		secret      sql.NullString
		pending     sql.NullString
		lastStep    sql.NullInt64
		failed      int64
		lockedUntil sql.NullInt64
	)
	if err := db.QueryRow(`
		SELECT totp_secret, totp_pending_secret, totp_last_step, failed_attempts, locked_until
		FROM admin_credentials WHERE id = 1
	`).Scan(&secret, &pending, &lastStep, &failed, &lockedUntil); err != nil {
		t.Fatalf("read admin after reset: %v", err)
	}
	if secret.Valid || pending.Valid || lastStep.Valid {
		t.Fatalf("TOTP columns = (%+v, %+v, %+v), want all NULL", secret, pending, lastStep)
	}
	if failed != 0 {
		t.Fatalf("failed_attempts = %d, want 0", failed)
	}
	if lockedUntil.Valid {
		t.Fatalf("locked_until = %+v, want NULL", lockedUntil)
	}

	var sessions int
	if err := db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 0 {
		t.Fatalf("sessions = %d, want 0", sessions)
	}
}

func TestResetTwoFactorPicksTheOnlyAccount(t *testing.T) {
	db := openResetDatabase(t)
	seedResetAdmin(t, db, sql.NullString{String: "SECRET", Valid: true})

	result, err := ResetTwoFactor(context.Background(), db, "")
	if err != nil {
		t.Fatalf("ResetTwoFactor() error = %v", err)
	}
	if result.Email != "admin@example.com" {
		t.Fatalf("Email = %q, want %q", result.Email, "admin@example.com")
	}
}

func TestResetTwoFactorEmailIsCaseInsensitive(t *testing.T) {
	db := openResetDatabase(t)
	seedResetAdmin(t, db, sql.NullString{String: "SECRET", Valid: true})

	result, err := ResetTwoFactor(context.Background(), db, " Admin@Example.COM ")
	if err != nil {
		t.Fatalf("ResetTwoFactor() error = %v", err)
	}
	if result.Email != "admin@example.com" {
		t.Fatalf("Email = %q, want %q", result.Email, "admin@example.com")
	}
	if !result.WasEnabled {
		t.Fatalf("WasEnabled = false, want true")
	}
}

func TestResetTwoFactorUnknownEmail(t *testing.T) {
	db := openResetDatabase(t)
	seedResetAdmin(t, db, sql.NullString{String: "SECRET", Valid: true})

	if _, err := ResetTwoFactor(context.Background(), db, "nobody@example.com"); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("ResetTwoFactor() error = %v, want ErrAccountNotFound", err)
	}
}

func TestResetTwoFactorWhenNotEnabledStillUnlocks(t *testing.T) {
	db := openResetDatabase(t)
	seedResetAdmin(t, db, sql.NullString{})

	result, err := ResetTwoFactor(context.Background(), db, "admin@example.com")
	if err != nil {
		t.Fatalf("ResetTwoFactor() error = %v", err)
	}
	if result.WasEnabled {
		t.Fatalf("WasEnabled = true, want false")
	}

	var lockedUntil sql.NullInt64
	if err := db.QueryRow("SELECT locked_until FROM admin_credentials WHERE id = 1").Scan(&lockedUntil); err != nil {
		t.Fatalf("read lock after reset: %v", err)
	}
	if lockedUntil.Valid {
		t.Fatalf("locked_until = %+v, want NULL", lockedUntil)
	}
}

func TestResetPasswordReplacesHashAndRevokesSessions(t *testing.T) {
	db := openResetDatabase(t)
	seedResetAdmin(t, db, sql.NullString{String: "SECRET", Valid: true})
	seedResetSession(t, db, "session-one")
	seedResetSession(t, db, "session-two")

	result, err := ResetPassword(context.Background(), db, "admin@example.com", "a brand new password")
	if err != nil {
		t.Fatalf("ResetPassword() error = %v", err)
	}
	want := ResetResult{Email: "admin@example.com", WasEnabled: true, SessionsRevoked: 2}
	if result != want {
		t.Fatalf("ResetPassword() = %+v, want %+v", result, want)
	}

	var hash string
	var secret sql.NullString
	var failed int
	var locked sql.NullInt64
	var fromEnv bool
	if err := db.QueryRow("SELECT password_hash, totp_secret, failed_attempts, locked_until, password_from_env FROM admin_credentials WHERE id = 1").
		Scan(&hash, &secret, &failed, &locked, &fromEnv); err != nil {
		t.Fatalf("read admin: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("a brand new password")) != nil {
		t.Fatal("stored hash does not match the new password")
	}
	if !secret.Valid {
		t.Fatal("two-factor was cleared; a password reset must keep it")
	}
	if failed != 0 || locked.Valid || fromEnv {
		t.Fatalf("failed = %d, locked = %v, fromEnv = %v; want a clean account", failed, locked, fromEnv)
	}
	var sessions int
	if err := db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("sessions = %d, err = %v; want 0", sessions, err)
	}
}

func TestResetPasswordRejectsShortPassword(t *testing.T) {
	db := openResetDatabase(t)
	seedResetAdmin(t, db, sql.NullString{})

	if _, err := ResetPassword(context.Background(), db, "", "short"); err == nil {
		t.Fatal("ResetPassword() accepted a short password")
	}
	var hash string
	if err := db.QueryRow("SELECT password_hash FROM admin_credentials").Scan(&hash); err != nil || hash != "hash" {
		t.Fatalf("hash = %q, err = %v; want it untouched", hash, err)
	}
}

func TestResetPasswordUnknownEmail(t *testing.T) {
	db := openResetDatabase(t)
	seedResetAdmin(t, db, sql.NullString{})

	if _, err := ResetPassword(context.Background(), db, "nobody@example.com", "long enough pw"); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("ResetPassword() error = %v, want ErrAccountNotFound", err)
	}
}

func TestChangeEmailReplacesTheAddressAndKeepsTheRest(t *testing.T) {
	db := openResetDatabase(t)
	seedResetAdmin(t, db, sql.NullString{String: "SECRET", Valid: true})
	seedResetSession(t, db, "session-one")

	result, err := ChangeEmail(context.Background(), db, "", "  New@Example.com ")
	if err != nil {
		t.Fatalf("ChangeEmail() error = %v", err)
	}
	want := ChangeEmailResult{OldEmail: "admin@example.com", NewEmail: "new@example.com"}
	if result != want {
		t.Fatalf("ChangeEmail() = %+v, want %+v", result, want)
	}

	var email, hash string
	var secret sql.NullString
	if err := db.QueryRow("SELECT email, password_hash, totp_secret FROM admin_credentials WHERE id = 1").Scan(&email, &hash, &secret); err != nil {
		t.Fatalf("read admin: %v", err)
	}
	if email != "new@example.com" || hash != "hash" || !secret.Valid {
		t.Fatalf("email = %q, hash = %q, secret = %v; want only the email changed", email, hash, secret)
	}
	var sessions int
	if err := db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("sessions = %d, err = %v; want 1", sessions, err)
	}
}

func TestChangeEmailRejectsInvalidAndUnknown(t *testing.T) {
	db := openResetDatabase(t)
	seedResetAdmin(t, db, sql.NullString{})

	if _, err := ChangeEmail(context.Background(), db, "", "not-an-email"); err == nil {
		t.Fatal("ChangeEmail() accepted an invalid email")
	}
	if _, err := ChangeEmail(context.Background(), db, "nobody@example.com", "new@example.com"); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("ChangeEmail() error = %v, want ErrAccountNotFound", err)
	}
	var email string
	if err := db.QueryRow("SELECT email FROM admin_credentials").Scan(&email); err != nil || email != "admin@example.com" {
		t.Fatalf("email = %q, err = %v; want it untouched", email, err)
	}
}
