package auth

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestSeedAdminCreatesTheFirstAdminOnce(t *testing.T) {
	db := openResetDatabase(t)
	ctx := context.Background()
	created, err := SeedAdmin(ctx, db, "Admin@Example.com", "correct horse battery staple", time.Now)
	if err != nil || !created {
		t.Fatalf("SeedAdmin = %v, %v; want created", created, err)
	}
	var email, hash string
	if err := db.QueryRow("SELECT email, password_hash FROM admin_credentials").Scan(&email, &hash); err != nil {
		t.Fatal(err)
	}
	if email != "admin@example.com" || bcrypt.CompareHashAndPassword([]byte(hash), []byte("correct horse battery staple")) != nil {
		t.Fatalf("seeded %q with a hash that does not match", email)
	}

	// A second boot, or one after the password was changed, leaves it alone.
	db.Exec("UPDATE admin_credentials SET password_hash = 'changed'")
	created, err = SeedAdmin(ctx, db, "admin@example.com", "another password here", time.Now)
	if err != nil || created {
		t.Fatalf("second SeedAdmin = %v, %v; want left alone", created, err)
	}
	db.QueryRow("SELECT password_hash FROM admin_credentials").Scan(&hash)
	if hash != "changed" {
		t.Fatal("SeedAdmin overwrote an existing admin's password")
	}
}

func TestSeedAdminWithNothingSetDoesNothing(t *testing.T) {
	db := openResetDatabase(t)
	if created, err := SeedAdmin(context.Background(), db, "", "", time.Now); err != nil || created {
		t.Fatalf("SeedAdmin with no env = %v, %v", created, err)
	}
}

func TestSeedAdminRefusesBadValuesWithoutEchoingThem(t *testing.T) {
	db := openResetDatabase(t)
	for name, tc := range map[string]struct{ email, password, wantVar string }{
		"email only":     {"admin@example.com", "", "RELIST_ADMIN_PASSWORD"},
		"password only":  {"", "correct horse battery staple", "RELIST_ADMIN_EMAIL"},
		"bad email":      {"not-an-email", "correct horse battery staple", "RELIST_ADMIN_EMAIL"},
		"short password": {"admin@example.com", "s3cr3t!", "RELIST_ADMIN_PASSWORD"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := SeedAdmin(context.Background(), db, tc.email, tc.password, time.Now)
			if err == nil || !strings.Contains(err.Error(), tc.wantVar) {
				t.Fatalf("error = %v, want one naming %s", err, tc.wantVar)
			}
			if tc.password != "" && strings.Contains(err.Error(), tc.password) {
				t.Fatalf("error %q echoes the password", err)
			}
		})
	}
}

func passwordFromEnvFlag(t *testing.T, db interface {
	QueryRow(string, ...any) *sql.Row
}) bool {
	t.Helper()
	var flag bool
	if err := db.QueryRow("SELECT password_from_env FROM admin_credentials").Scan(&flag); err != nil {
		t.Fatal(err)
	}
	return flag
}

// Whether the password is still the env's is recorded, so the account page
// never reads the env or runs bcrypt per request (ADR-0003: env in Config).
func TestSeedAdminRecordsThePasswordAsFromEnv(t *testing.T) {
	db := openResetDatabase(t)
	if _, err := SeedAdmin(context.Background(), db, "admin@example.com", "correct horse battery staple", time.Now); err != nil {
		t.Fatal(err)
	}
	if !passwordFromEnvFlag(t, db) {
		t.Fatal("a seeded admin's password is not recorded as from the env")
	}
}

// An admin seeded before the flag existed is recognised at boot while its
// password still matches the env.
func TestSeedAdminRecognisesAnEarlierSeededPassword(t *testing.T) {
	db := openResetDatabase(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery staple"), bcrypt.MinCost)
	db.Exec("INSERT INTO admin_credentials (id, email, password_hash, created_at) VALUES (1, 'admin@example.com', ?, 1)", string(hash))
	if _, err := SeedAdmin(context.Background(), db, "admin@example.com", "another password here", time.Now); err != nil {
		t.Fatal(err)
	}
	if passwordFromEnvFlag(t, db) {
		t.Fatal("a password the env does not hold was recorded as from the env")
	}
	if _, err := SeedAdmin(context.Background(), db, "admin@example.com", "correct horse battery staple", time.Now); err != nil {
		t.Fatal(err)
	}
	if !passwordFromEnvFlag(t, db) {
		t.Fatal("an earlier seeded password was not recognised")
	}
}
