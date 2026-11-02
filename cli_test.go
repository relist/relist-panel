package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

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

// fakeTerminal answers hidden reads from a queue, as the real terminal would
// one Enter at a time.
func fakeTerminal(passwords ...string) console {
	return console{
		isTerminal: true,
		readPassword: func() (string, error) {
			if len(passwords) == 0 {
				return "", io.EOF
			}
			next := passwords[0]
			passwords = passwords[1:]
			return next, nil
		},
	}
}

var noTerminal = console{readPassword: func() (string, error) { return "", io.EOF }}

// storedAdmin reads back the single admin row.
func storedAdmin(t *testing.T) (email, hash string, fromEnv bool) {
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
	if err := db.QueryRow("SELECT email, password_hash, password_from_env FROM admin_credentials").Scan(&email, &hash, &fromEnv); err != nil {
		t.Fatalf("read admin: %v", err)
	}
	return email, hash, fromEnv
}

func runCreate(t *testing.T, args []string, stdin string, term console) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runCreateAdmin(args, strings.NewReader(stdin), &stdout, &stderr, term)
	return code, stdout.String(), stderr.String()
}

func TestCreateAdminFromPasswordStdin(t *testing.T) {
	t.Setenv("RELIST_DATA_DIR", t.TempDir())

	code, stdout, stderr := runCreate(t, []string{"--email", "a@example.com", "--password-stdin"}, "long enough pw\r\n", noTerminal)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}
	if stdout != "Created admin a@example.com.\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	email, hash, fromEnv := storedAdmin(t)
	if email != "a@example.com" || fromEnv {
		t.Fatalf("admin = %q, password_from_env = %v", email, fromEnv)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("long enough pw")) != nil {
		t.Fatal("stored hash does not match the password from stdin")
	}
}

func TestCreateAdminGenerated(t *testing.T) {
	t.Setenv("RELIST_DATA_DIR", t.TempDir())

	code, stdout, stderr := runCreate(t, []string{"--email", "A@Example.com", "--generate"}, "", noTerminal)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if len(lines) != 2 || lines[0] != "Created admin a@example.com." || !strings.HasPrefix(lines[1], "Password: ") {
		t.Fatalf("stdout = %q", stdout)
	}
	password := strings.TrimPrefix(lines[1], "Password: ")
	if len(password) != 24 {
		t.Fatalf("generated password %q has %d characters, want 24", password, len(password))
	}
	if !strings.Contains(stderr, "change it in Account") {
		t.Fatalf("stderr = %q, want the hint", stderr)
	}
	email, hash, _ := storedAdmin(t)
	if email != "a@example.com" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		t.Fatalf("admin = %q, or the hash does not match the printed password", email)
	}
}

func TestCreateAdminRefusesWhenOneExists(t *testing.T) {
	t.Setenv("RELIST_DATA_DIR", t.TempDir())
	seedCLIAdmin(t)

	code, _, stderr := runCreate(t, []string{"--email", "b@example.com", "--password-stdin"}, "long enough pw\n", noTerminal)
	if code != 1 || !strings.Contains(stderr, "create-admin: an admin account already exists") {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	if email, hash, _ := storedAdmin(t); email != "admin@example.com" || hash != "hash" {
		t.Fatalf("admin changed to %q", email)
	}
}

func TestCreateAdminUsageErrors(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"both password flags":            {[]string{"--email", "a@example.com", "--generate", "--password-stdin"}, "usage: relist-panel create-admin"},
		"password-stdin without email":   {[]string{"--password-stdin"}, "usage: relist-panel create-admin"},
		"stray argument":                 {[]string{"extra"}, "usage: relist-panel create-admin"},
		"no terminal and no flag":        {[]string{"--email", "a@example.com"}, "create-admin: stdin is not a terminal; pass --password-stdin or --generate"},
		"no terminal, generate no email": {[]string{"--generate"}, "create-admin: stdin is not a terminal; pass --password-stdin or --generate"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("RELIST_DATA_DIR", t.TempDir())
			code, _, stderr := runCreate(t, c.args, "", noTerminal)
			if code != 2 || !strings.Contains(stderr, c.want) {
				t.Fatalf("exit code = %d, stderr = %q; want 2 and %q", code, stderr, c.want)
			}
		})
	}
}

func TestCreateAdminRejectsShortPassword(t *testing.T) {
	t.Setenv("RELIST_DATA_DIR", t.TempDir())

	code, _, stderr := runCreate(t, []string{"--email", "a@example.com", "--password-stdin"}, "short\n", noTerminal)
	if code != 1 || !strings.Contains(stderr, "create-admin: password must be at least 8 characters") {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
}

func TestCreateAdminInteractive(t *testing.T) {
	t.Run("typed and confirmed", func(t *testing.T) {
		t.Setenv("RELIST_DATA_DIR", t.TempDir())
		code, stdout, stderr := runCreate(t, nil, "a@example.com\n", fakeTerminal("long enough pw", "long enough pw"))
		if code != 0 || stdout != "Created admin a@example.com.\n" {
			t.Fatalf("exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
		}
		for _, prompt := range []string{"Email: ", "Password (leave empty to generate one): ", "Confirm password: "} {
			if !strings.Contains(stderr, prompt) {
				t.Fatalf("stderr = %q, want the prompt %q", stderr, prompt)
			}
		}
		if _, hash, _ := storedAdmin(t); bcrypt.CompareHashAndPassword([]byte(hash), []byte("long enough pw")) != nil {
			t.Fatal("stored hash does not match the typed password")
		}
	})
	t.Run("mismatched confirmation", func(t *testing.T) {
		t.Setenv("RELIST_DATA_DIR", t.TempDir())
		code, _, stderr := runCreate(t, nil, "a@example.com\n", fakeTerminal("long enough pw", "something else"))
		if code != 1 || !strings.Contains(stderr, "create-admin: passwords do not match") {
			t.Fatalf("exit code = %d, stderr = %q", code, stderr)
		}
	})
	t.Run("empty password generates one", func(t *testing.T) {
		t.Setenv("RELIST_DATA_DIR", t.TempDir())
		code, stdout, stderr := runCreate(t, []string{"--email", "a@example.com"}, "", fakeTerminal(""))
		if code != 0 || !strings.Contains(stdout, "\nPassword: ") {
			t.Fatalf("exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
		}
		if strings.Contains(stderr, "Email: ") || strings.Contains(stderr, "Confirm password: ") {
			t.Fatalf("stderr = %q, want neither the email nor the confirm prompt", stderr)
		}
	})
	t.Run("an existing admin is reported before any prompt", func(t *testing.T) {
		t.Setenv("RELIST_DATA_DIR", t.TempDir())
		seedCLIAdmin(t)
		code, _, stderr := runCreate(t, nil, "", fakeTerminal())
		if code != 1 || strings.Contains(stderr, "Email: ") {
			t.Fatalf("exit code = %d, stderr = %q", code, stderr)
		}
	})
}

func runResetPw(t *testing.T, args []string, stdin string, term console) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runResetPassword(args, strings.NewReader(stdin), &stdout, &stderr, term)
	return code, stdout.String(), stderr.String()
}

func TestResetPasswordCommandFromStdin(t *testing.T) {
	t.Setenv("RELIST_DATA_DIR", t.TempDir())
	seedCLIAdmin(t)

	code, stdout, stderr := runResetPw(t, []string{"--password-stdin"}, "long enough pw\n", noTerminal)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}
	if stdout != "Reset password for admin@example.com; revoked 2 sessions.\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	_, hash, _ := storedAdmin(t)
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("long enough pw")) != nil {
		t.Fatal("stored hash does not match the password from stdin")
	}
}

func TestResetPasswordCommandGenerated(t *testing.T) {
	t.Setenv("RELIST_DATA_DIR", t.TempDir())
	seedCLIAdmin(t)

	code, stdout, stderr := runResetPw(t, []string{"--generate"}, "", noTerminal)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}
	_, password, found := strings.Cut(stdout, "Password: ")
	password = strings.TrimSpace(password)
	if !found || password == "" {
		t.Fatalf("stdout = %q, want the generated password", stdout)
	}
	_, hash, _ := storedAdmin(t)
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		t.Fatal("stored hash does not match the printed password")
	}
}

func TestResetPasswordCommandPromptMismatch(t *testing.T) {
	t.Setenv("RELIST_DATA_DIR", t.TempDir())
	seedCLIAdmin(t)

	code, _, stderr := runResetPw(t, nil, "", fakeTerminal("long enough pw", "something else"))
	if code != 1 || !strings.Contains(stderr, "do not match") {
		t.Fatalf("exit code = %d, stderr = %q; want 1 and a mismatch", code, stderr)
	}
	if _, hash, _ := storedAdmin(t); hash != "hash" {
		t.Fatal("hash changed despite the mismatch")
	}
}

func TestResetPasswordCommandNeedsAModeWithoutTerminal(t *testing.T) {
	t.Setenv("RELIST_DATA_DIR", t.TempDir())

	if code, _, stderr := runResetPw(t, nil, "", noTerminal); code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr = %q", code, stderr)
	}
}

func TestResetPasswordCommandUnknownEmail(t *testing.T) {
	t.Setenv("RELIST_DATA_DIR", t.TempDir())
	seedCLIAdmin(t)

	code, _, stderr := runResetPw(t, []string{"--email", "nobody@example.com", "--generate"}, "", noTerminal)
	if code != 1 || !strings.Contains(stderr, "nobody@example.com") {
		t.Fatalf("exit code = %d, stderr = %q; want 1 and the email", code, stderr)
	}
}

func runChangeEmailCmd(t *testing.T, args []string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runChangeEmail(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestChangeEmailCommand(t *testing.T) {
	t.Setenv("RELIST_DATA_DIR", t.TempDir())
	seedCLIAdmin(t)

	code, stdout, stderr := runChangeEmailCmd(t, []string{"--new-email", "New@Example.com"})
	if code != 0 || stdout != "Changed email from admin@example.com to new@example.com.\n" {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if email, _, _ := storedAdmin(t); email != "new@example.com" {
		t.Fatalf("stored email = %q", email)
	}
}

func TestChangeEmailCommandUsageAndErrors(t *testing.T) {
	t.Setenv("RELIST_DATA_DIR", t.TempDir())
	seedCLIAdmin(t)

	if code, _, _ := runChangeEmailCmd(t, nil); code != 2 {
		t.Fatalf("no --new-email: exit code = %d, want 2", code)
	}
	if code, _, stderr := runChangeEmailCmd(t, []string{"--new-email", "bad"}); code != 1 {
		t.Fatalf("invalid email: exit code = %d, stderr = %q; want 1", code, stderr)
	}
	code, _, stderr := runChangeEmailCmd(t, []string{"--email", "nobody@example.com", "--new-email", "new@example.com"})
	if code != 1 || !strings.Contains(stderr, "nobody@example.com") {
		t.Fatalf("unknown email: exit code = %d, stderr = %q", code, stderr)
	}
}
