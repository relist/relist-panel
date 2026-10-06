package auth

import (
	"database/sql"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// testSecret generates a fresh base32 secret the way the setup route does, so
// the unit tests exercise the same code shape the handlers use.
func testSecret(t *testing.T) string {
	t.Helper()
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Relist Panel", AccountName: "admin@example.com"})
	if err != nil {
		t.Fatalf("generate TOTP secret: %v", err)
	}
	return key.Secret()
}

func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{
		Period:    30,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	return code
}

func TestVerifyCodeAcceptsOneStepOfSkew(t *testing.T) {
	secret := testSecret(t)
	now := time.Date(2026, 1, 1, 12, 0, 15, 0, time.UTC)

	for _, offset := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		code := codeAt(t, secret, now.Add(offset))
		if _, ok := verifyCode(secret, code, now, sql.NullInt64{}); !ok {
			t.Fatalf("code at offset %s was refused, want accepted", offset)
		}
	}

	for _, offset := range []time.Duration{-60 * time.Second, 60 * time.Second} {
		code := codeAt(t, secret, now.Add(offset))
		if _, ok := verifyCode(secret, code, now, sql.NullInt64{}); ok {
			t.Fatalf("code at offset %s was accepted, want refused", offset)
		}
	}
}

func TestVerifyCodeRefusesReplayedSteps(t *testing.T) {
	secret := testSecret(t)
	now := time.Date(2026, 1, 1, 12, 0, 15, 0, time.UTC)
	code := codeAt(t, secret, now)
	step := now.Unix() / 30

	if _, ok := verifyCode(secret, code, now, sql.NullInt64{Int64: step, Valid: true}); ok {
		t.Fatalf("code at its own step was accepted, want refused")
	}
	if _, ok := verifyCode(secret, code, now, sql.NullInt64{Int64: step + 1, Valid: true}); ok {
		t.Fatalf("code one step behind lastStep was accepted, want refused")
	}
}

func TestVerifyCodeReturnsTheMatchedStep(t *testing.T) {
	secret := testSecret(t)
	now := time.Date(2026, 1, 1, 12, 0, 15, 0, time.UTC)
	code := codeAt(t, secret, now.Add(-30*time.Second))

	step, ok := verifyCode(secret, code, now, sql.NullInt64{})
	if !ok {
		t.Fatalf("code one step old was refused, want accepted")
	}
	if want := now.Unix()/30 - 1; step != want {
		t.Fatalf("matched step = %d, want %d", step, want)
	}
}
