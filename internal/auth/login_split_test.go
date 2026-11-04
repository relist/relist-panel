package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/database"
)

const splitPassword = "correct horse battery staple"

func newSplitService(t *testing.T) (*Service, http.Handler, func() int) {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := CreateAdmin(context.Background(), db, "admin@example.com", splitPassword, time.Now); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	service := New(db)
	mux := http.NewServeMux()
	service.RegisterRoutes(mux)
	sessions := func() int {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&n); err != nil {
			t.Fatalf("count sessions: %v", err)
		}
		return n
	}
	return service, mux, sessions
}

func splitLogin(handler http.Handler, password string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"email": "admin@example.com", "password": password})
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestLoginDoesNotHoldTheDatabaseDuringThePasswordCheck(t *testing.T) {
	service, handler, _ := newSplitService(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	service.comparePassword = func(hash, password []byte) error {
		close(entered)
		<-release
		return nil
	}

	done := make(chan int, 1)
	go func() { done <- splitLogin(handler, splitPassword).Code }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("login never reached the password check")
	}

	queried := make(chan error, 1)
	go func() {
		var one int
		queried <- service.db.QueryRow("SELECT 1").Scan(&one)
	}()
	select {
	case err := <-queried:
		if err != nil {
			t.Fatalf("unrelated query: %v", err)
		}
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("an unrelated query waited on a login's password check")
	}

	close(release)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("login status = %d, want %d", code, http.StatusOK)
	}
}

func TestParallelWrongLoginsNeverRecordMoreFailuresThanSerialOnes(t *testing.T) {
	service, handler, _ := newSplitService(t)
	const attempts = maxFailedAttempts * 4

	codes := make(chan int, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- splitLogin(handler, "wrong password").Code
		}()
	}
	wg.Wait()
	close(codes)

	rejected, locked := 0, 0
	for code := range codes {
		switch code {
		case http.StatusUnauthorized:
			rejected++
		case http.StatusTooManyRequests:
			locked++
		default:
			t.Fatalf("unexpected status %d", code)
		}
	}
	// Serial attempts: the first maxFailedAttempts answer 401 (the last sets
	// the lock), every later one answers 429.
	if rejected != maxFailedAttempts || locked != attempts-maxFailedAttempts {
		t.Fatalf("401s = %d, 429s = %d; want %d and %d", rejected, locked, maxFailedAttempts, attempts-maxFailedAttempts)
	}
	var failed int
	var lockedUntil *int64
	if err := service.db.QueryRow("SELECT failed_attempts, locked_until FROM users").Scan(&failed, &lockedUntil); err != nil {
		t.Fatalf("read account: %v", err)
	}
	if failed != 0 || lockedUntil == nil {
		t.Fatalf("failed_attempts = %d, locked_until = %v; want 0 and a lock", failed, lockedUntil)
	}
}

func TestLoginFailsWhenThePasswordChangesDuringTheCheck(t *testing.T) {
	service, handler, sessions := newSplitService(t)
	service.comparePassword = func(hash, password []byte) error {
		if _, err := service.db.Exec("UPDATE users SET password_hash = 'changed'"); err != nil {
			t.Errorf("change password: %v", err)
		}
		return nil
	}

	recorder := splitLogin(handler, splitPassword)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	if n := sessions(); n != 0 {
		t.Fatalf("sessions = %d, want 0", n)
	}
	var failed int
	if err := service.db.QueryRow("SELECT failed_attempts FROM users").Scan(&failed); err != nil || failed != 0 {
		t.Fatalf("failed_attempts = %d (err %v), want 0", failed, err)
	}
}
