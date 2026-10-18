package migration

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"relist-panel/internal/database"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock                   { return &clock{t: time.Unix(1_800_000_000, 0).UTC()} }

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newTestStore(t *testing.T) (*Store, *clock, *sql.DB) {
	t.Helper()
	db := openDB(t)
	c := newClock()
	return NewStore(db, c.now), c, db
}

func TestCreateTokenAcceptsOnlyThreeLifetimes(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	for _, ok := range []time.Duration{time.Hour, 24 * time.Hour, 168 * time.Hour} {
		if _, _, err := s.CreateToken(ctx, ok); err != nil {
			t.Fatalf("CreateToken(%v) error = %v", ok, err)
		}
	}
	for _, bad := range []time.Duration{2 * time.Hour, 169 * time.Hour, 0} {
		if _, _, err := s.CreateToken(ctx, bad); !errors.Is(err, ErrBadLifetime) {
			t.Fatalf("CreateToken(%v) error = %v, want ErrBadLifetime", bad, err)
		}
	}
}

func TestCreateTokenRevokesThePreviousOne(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	first, _, _ := s.CreateToken(ctx, time.Hour)
	if _, _, err := s.CreateToken(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(ctx, first, "https://new"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Claim(first) error = %v, want ErrInvalidToken", err)
	}
}

func TestTokenExpires(t *testing.T) {
	s, c, _ := newTestStore(t)
	ctx := context.Background()
	token, expires, _ := s.CreateToken(ctx, time.Hour)
	if !expires.Equal(c.now().Add(time.Hour)) {
		t.Fatalf("expiresAt = %v, want %v", expires, c.now().Add(time.Hour))
	}
	c.advance(time.Hour + time.Second)
	if _, err := s.Claim(ctx, token, "https://new"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Claim() error = %v, want ErrInvalidToken", err)
	}
}

func TestClaimIsSingleUse(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	token, _, _ := s.CreateToken(ctx, time.Hour)
	key, err := s.Claim(ctx, token, "https://new")
	if err != nil || key == "" {
		t.Fatalf("Claim() = %q, %v", key, err)
	}
	if _, err := s.Claim(ctx, token, "https://other"); !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("second Claim() error = %v, want ErrAlreadyClaimed", err)
	}
	if err := s.VerifySession(ctx, key); err != nil {
		t.Fatalf("VerifySession(key) error = %v", err)
	}
	if err := s.VerifySession(ctx, key+"x"); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("VerifySession(wrong) error = %v, want ErrInvalidSession", err)
	}
	if _, claimedBy, _ := s.TokenStatus(ctx); claimedBy != "https://new" {
		t.Fatalf("claimedBy = %q", claimedBy)
	}
}

func TestClaimLocksAfterFiveWrongTokens(t *testing.T) {
	s, c, db := newTestStore(t)
	ctx := context.Background()
	token, _, _ := s.CreateToken(ctx, time.Hour)
	for i := 0; i < 5; i++ {
		if _, err := s.Claim(ctx, "wrong", "x"); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("wrong claim %d error = %v", i, err)
		}
	}
	if _, err := s.Claim(ctx, token, "x"); !errors.Is(err, ErrClaimLocked) {
		t.Fatalf("locked Claim() error = %v, want ErrClaimLocked", err)
	}
	c.advance(61 * time.Second)
	if _, err := s.Claim(ctx, token, "x"); err != nil {
		t.Fatalf("Claim() after lock error = %v", err)
	}
	var failures int
	if err := db.QueryRow("SELECT failures FROM migration_claim_failures").Scan(&failures); err != nil || failures != 0 {
		t.Fatalf("failures = %d, %v; want 0", failures, err)
	}
}

func TestOnlyHashesAreStored(t *testing.T) {
	s, _, db := newTestStore(t)
	ctx := context.Background()
	token, _, _ := s.CreateToken(ctx, time.Hour)
	key, _ := s.Claim(ctx, token, "x")
	var tokenHash, sessionHash string
	if err := db.QueryRow("SELECT token_hash, session_hash FROM migration_token").Scan(&tokenHash, &sessionHash); err != nil {
		t.Fatal(err)
	}
	for _, stored := range []string{tokenHash, sessionHash} {
		if strings.Contains(stored, token) || strings.Contains(stored, key) {
			t.Fatalf("stored %q contains a secret", stored)
		}
	}
}

func TestRevokeAfterStartIsRefused(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	if _, _, err := s.CreateToken(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLock(ctx, StepLocked, LockState{}); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeToken(ctx); !errors.Is(err, ErrStarted) {
		t.Fatalf("RevokeToken() error = %v, want ErrStarted", err)
	}
}

func TestLockRoundTrips(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	if step, _, err := s.Lock(ctx); err != nil || step != StepNone {
		t.Fatalf("Lock() = %q, %v; want none", step, err)
	}
	want := LockState{RunningComposeIDs: []int64{3, 7}, HostPaths: []string{"/srv/a"}, ClaimedBy: "https://new", LockedAt: 42}
	if err := s.SetLock(ctx, StepStopping, want); err != nil {
		t.Fatal(err)
	}
	step, got, err := s.Lock(ctx)
	if err != nil || step != StepStopping || !reflect.DeepEqual(got, want) {
		t.Fatalf("Lock() = %q, %+v, %v; want %+v", step, got, err, want)
	}
	if err := s.ClearAll(ctx); err != nil {
		t.Fatal(err)
	}
	if step, _, _ := s.Lock(ctx); step != StepNone {
		t.Fatalf("Lock() after ClearAll = %q", step)
	}
}

func eventuallyTrue(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
