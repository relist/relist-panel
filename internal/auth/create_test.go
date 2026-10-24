package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestCreateAdminStoresTheFirstAdmin(t *testing.T) {
	db := openResetDatabase(t)
	ctx := context.Background()
	if exists, err := AdminExists(ctx, db); err != nil || exists {
		t.Fatalf("AdminExists on an empty panel = %v, %v", exists, err)
	}
	email, err := CreateAdmin(ctx, db, " Admin@Example.com ", "correct horse battery staple", time.Now)
	if err != nil || email != "admin@example.com" {
		t.Fatalf("CreateAdmin = %q, %v", email, err)
	}
	var hash string
	var fromEnv bool
	if err := db.QueryRow("SELECT password_hash, password_from_env FROM admin_credentials").Scan(&hash, &fromEnv); err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("correct horse battery staple")) != nil || fromEnv {
		t.Fatalf("hash does not match, or password_from_env = %v", fromEnv)
	}
	if exists, err := AdminExists(ctx, db); err != nil || !exists {
		t.Fatalf("AdminExists after CreateAdmin = %v, %v", exists, err)
	}

	if _, err := CreateAdmin(ctx, db, "other@example.com", "another password here", time.Now); !errors.Is(err, ErrAdminAlreadyExists) {
		t.Fatalf("second CreateAdmin error = %v, want ErrAdminAlreadyExists", err)
	}
}

func TestCreateAdminValidates(t *testing.T) {
	db := openResetDatabase(t)
	ctx := context.Background()
	if _, err := CreateAdmin(ctx, db, "not an email", "correct horse battery staple", time.Now); err == nil {
		t.Fatal("CreateAdmin accepted an invalid email")
	}
	if _, err := CreateAdmin(ctx, db, "admin@example.com", "short", time.Now); err == nil || err.Error() != "password must be at least 8 characters" {
		t.Fatalf("CreateAdmin short password error = %v", err)
	}
	if exists, _ := AdminExists(ctx, db); exists {
		t.Fatal("a refused CreateAdmin stored an admin")
	}
}

func TestGeneratePassword(t *testing.T) {
	first, err := GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}
	second, _ := GeneratePassword()
	if len(first) != 24 || first == second {
		t.Fatalf("GeneratePassword = %q, %q; want 24 characters, different each time", first, second)
	}
	if err := validatePassword(first); err != nil {
		t.Fatalf("generated password fails validation: %v", err)
	}
}
