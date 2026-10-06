package main

import (
	"bytes"
	"strings"
	"testing"

	"relist-panel/internal/config"
	"relist-panel/internal/database"
)

// seedCLIAdmin creates the panel database under the data directory chosen by
// the test and fills the single admin row with two-factor state, a lock and
// two sessions, so runResetTwoFactor has something to clear.
func seedCLIAdmin(t *testing.T) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	db, err := database.OpenAndMigrate(cfg.DatabasePath())
	if err != nil {
		t.Fatalf("OpenAndMigrate() error = %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		INSERT INTO admin_credentials
			(id, email, password_hash, created_at, totp_secret, totp_pending_secret, totp_last_step, failed_attempts, locked_until)
		VALUES (1, 'admin@example.com', 'hash', 1, 'SECRET', 'PENDING', 42, 3, 9999999999)
	`); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO sessions (token, admin_id, expires_at, created_at)
		VALUES ('session-one', 1, 9999999999, 1), ('session-two', 1, 9999999999, 1)
	`); err != nil {
		t.Fatalf("seed sessions: %v", err)
	}
}

func TestResetTwoFactorCommandSucceeds(t *testing.T) {
	t.Setenv("RELIST_DATA_DIR", t.TempDir())
	seedCLIAdmin(t)

	var stdout, stderr bytes.Buffer
	if code := runResetTwoFactor(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "admin@example.com") {
		t.Fatalf("stdout = %q, want the email", stdout.String())
	}
	if !strings.Contains(stdout.String(), "2 sessions") {
		t.Fatalf("stdout = %q, want the revoked session count", stdout.String())
	}
}

func TestResetTwoFactorCommandUnknownEmail(t *testing.T) {
	t.Setenv("RELIST_DATA_DIR", t.TempDir())
	seedCLIAdmin(t)

	var stdout, stderr bytes.Buffer
	if code := runResetTwoFactor([]string{"--email", "nobody@example.com"}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "nobody@example.com") {
		t.Fatalf("stderr = %q, want the email", stderr.String())
	}
}

func TestResetTwoFactorCommandRejectsStrayArgs(t *testing.T) {
	t.Setenv("RELIST_DATA_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	if code := runResetTwoFactor([]string{"extra"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: relist-panel reset-2fa [--email ADDRESS]") {
		t.Fatalf("stderr = %q, want the usage line", stderr.String())
	}
}
