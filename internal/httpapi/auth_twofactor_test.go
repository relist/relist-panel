package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"relist-panel/internal/auth"
)

func TestFiveFailedLoginsLockTheAccount(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	for attempt := 1; attempt <= 5; attempt++ {
		response := postJSON(t, server, "/api/auth/login", map[string]string{
			"email":    "admin@example.com",
			"password": "wrong password",
		})
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("wrong attempt %d status = %d, want %d", attempt, response.StatusCode, http.StatusUnauthorized)
		}
	}

	locked := postJSON(t, server, "/api/auth/login", map[string]string{
		"email":    "admin@example.com",
		"password": "correct horse battery staple",
	})
	defer locked.Body.Close()
	if locked.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("correct password while locked status = %d, want %d", locked.StatusCode, http.StatusTooManyRequests)
	}

	retryAfter, err := strconv.Atoi(locked.Header.Get("Retry-After"))
	if err != nil {
		t.Fatalf("Retry-After = %q, want an integer: %v", locked.Header.Get("Retry-After"), err)
	}
	if retryAfter < 1 || retryAfter > 60 {
		t.Fatalf("Retry-After = %d, want a value between 1 and 60", retryAfter)
	}

	var body map[string]string
	if err := json.NewDecoder(locked.Body).Decode(&body); err != nil {
		t.Fatalf("decode locked body: %v", err)
	}
	if !strings.HasPrefix(body["error"], "too many attempts") {
		t.Fatalf("locked error = %q, want a prefix of %q", body["error"], "too many attempts")
	}
}

func TestSuccessfulLoginResetsTheFailureCount(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	wrongLogin := func(attempt int) {
		t.Helper()
		response := postJSON(t, server, "/api/auth/login", map[string]string{
			"email":    "admin@example.com",
			"password": "wrong password",
		})
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("wrong attempt %d status = %d, want %d", attempt, response.StatusCode, http.StatusUnauthorized)
		}
	}

	for attempt := 1; attempt <= 4; attempt++ {
		wrongLogin(attempt)
	}
	if got := failedAttempts(t, server); got != 4 {
		t.Fatalf("failed_attempts after four wrong logins = %d, want 4", got)
	}

	correct := postJSON(t, server, "/api/auth/login", map[string]string{
		"email":    "admin@example.com",
		"password": "correct horse battery staple",
	})
	correct.Body.Close()
	if correct.StatusCode != http.StatusOK {
		t.Fatalf("correct login status = %d, want %d", correct.StatusCode, http.StatusOK)
	}
	if got := failedAttempts(t, server); got != 0 {
		t.Fatalf("failed_attempts after a successful login = %d, want 0", got)
	}

	for attempt := 5; attempt <= 8; attempt++ {
		wrongLogin(attempt)
	}
	if got := failedAttempts(t, server); got != 4 {
		t.Fatalf("failed_attempts after four more wrong logins = %d, want 4", got)
	}
}

// failedAttempts reads the admin row's consecutive-failure counter directly,
// the same way TestAuthSetupAndLogin reads the stored password hash.
func failedAttempts(t *testing.T, server *testServer) int {
	t.Helper()
	var count int
	if err := server.db.QueryRow("SELECT failed_attempts FROM admin_credentials").Scan(&count); err != nil {
		t.Fatalf("read failed_attempts: %v", err)
	}
	return count
}

func TestUnknownEmailIsNotCounted(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	for attempt := 1; attempt <= 6; attempt++ {
		response := postJSON(t, server, "/api/auth/login", map[string]string{
			"email":    "nobody@example.com",
			"password": "correct horse battery staple",
		})
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unknown-email attempt %d status = %d, want %d", attempt, response.StatusCode, http.StatusUnauthorized)
		}
	}
	if got := failedAttempts(t, server); got != 0 {
		t.Fatalf("failed_attempts after unknown-email attempts = %d, want 0", got)
	}

	response := postJSON(t, server, "/api/auth/login", map[string]string{
		"email":    "admin@example.com",
		"password": "correct horse battery staple",
	})
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("real admin login status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}

const (
	twoFactorTestEmail    = "admin@example.com"
	twoFactorTestPassword = "correct horse battery staple"
)

// codeAtTime mints a TOTP code with the exact parameters the server verifies:
// SHA-1, six digits, thirty-second period.
func codeAtTime(t *testing.T, secret string, at time.Time) string {
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

// enableTwoFactor runs the setup and enable flow against the server's current
// session and returns the base32 secret. It leaves the account with two-factor
// on and the current step consumed.
func enableTwoFactor(t *testing.T, server *testServer) string {
	t.Helper()

	setup := postJSON(t, server, "/api/auth/2fa/setup", map[string]string{"password": twoFactorTestPassword})
	defer setup.Body.Close()
	if setup.StatusCode != http.StatusOK {
		t.Fatalf("2fa setup status = %d, want %d", setup.StatusCode, http.StatusOK)
	}
	var body struct {
		Secret     string `json:"secret"`
		OtpauthURI string `json:"otpauthUri"`
	}
	if err := json.NewDecoder(setup.Body).Decode(&body); err != nil {
		t.Fatalf("decode setup body: %v", err)
	}
	if body.Secret == "" || body.OtpauthURI == "" {
		t.Fatalf("setup body = %+v, want a secret and an otpauth URI", body)
	}

	enable := postJSON(t, server, "/api/auth/2fa/enable", map[string]string{
		"code": codeAtTime(t, body.Secret, time.Now()),
	})
	defer enable.Body.Close()
	if enable.StatusCode != http.StatusNoContent {
		t.Fatalf("2fa enable status = %d, want %d", enable.StatusCode, http.StatusNoContent)
	}
	return body.Secret
}

type twoFactorAccount struct {
	Email            string `json:"email"`
	TwoFactorEnabled bool   `json:"twoFactorEnabled"`
}

func getTwoFactorAccount(t *testing.T, server *testServer) twoFactorAccount {
	t.Helper()
	response := server.request(t, http.MethodGet, "/api/auth/account", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("account status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var account twoFactorAccount
	if err := json.NewDecoder(response.Body).Decode(&account); err != nil {
		t.Fatalf("decode account body: %v", err)
	}
	return account
}

// sessionToken returns the session cookie the server currently holds.
func sessionToken(t *testing.T, server *testServer) string {
	t.Helper()
	cookie, ok := server.cookies[auth.SessionCookieName]
	if !ok || cookie.Value == "" {
		t.Fatalf("no session cookie stored")
	}
	return cookie.Value
}

// getWithSession sends an authenticated GET with an explicit session token, so
// a test can compare two sessions without sharing the server's cookie map.
func getWithSession(t *testing.T, server *testServer, path, token string) *http.Response {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://relist-panel.test"+path, nil)
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	recorder := httptest.NewRecorder()
	server.handler.ServeHTTP(recorder, request)
	return recorder.Result()
}

func loginWithCode(t *testing.T, server *testServer, code string) *http.Response {
	t.Helper()
	return postJSON(t, server, "/api/auth/login", map[string]string{
		"email":    twoFactorTestEmail,
		"password": twoFactorTestPassword,
		"code":     code,
	})
}

func TestTwoFactorRoundTrip(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	if account := getTwoFactorAccount(t, server); account.TwoFactorEnabled {
		t.Fatalf("twoFactorEnabled before enable = true, want false")
	}

	secret := enableTwoFactor(t, server)
	account := getTwoFactorAccount(t, server)
	if !account.TwoFactorEnabled {
		t.Fatalf("twoFactorEnabled after enable = false, want true")
	}
	if account.Email != twoFactorTestEmail {
		t.Fatalf("account email = %q, want %q", account.Email, twoFactorTestEmail)
	}

	logout := server.request(t, http.MethodPost, "/api/auth/logout", nil)
	logout.Body.Close()
	if logout.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d, want %d", logout.StatusCode, http.StatusNoContent)
	}

	noCode := postJSON(t, server, "/api/auth/login", map[string]string{
		"email":    twoFactorTestEmail,
		"password": twoFactorTestPassword,
	})
	defer noCode.Body.Close()
	if noCode.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login without a code status = %d, want %d", noCode.StatusCode, http.StatusUnauthorized)
	}
	var noCodeBody struct {
		Error        string `json:"error"`
		CodeRequired bool   `json:"codeRequired"`
	}
	if err := json.NewDecoder(noCode.Body).Decode(&noCodeBody); err != nil {
		t.Fatalf("decode code-required body: %v", err)
	}
	if !noCodeBody.CodeRequired {
		t.Fatalf("codeRequired = false, want true")
	}
	if noCodeBody.Error != "two-factor code required" {
		t.Fatalf("code-required error = %q, want %q", noCodeBody.Error, "two-factor code required")
	}
	if got := failedAttempts(t, server); got != 0 {
		t.Fatalf("failed_attempts after a code-required answer = %d, want 0", got)
	}

	login := loginWithCode(t, server, codeAtTime(t, secret, time.Now().Add(30*time.Second)))
	defer login.Body.Close()
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login with the next step's code status = %d, want %d", login.StatusCode, http.StatusOK)
	}
}

func TestTwoFactorSetupRequiresThePassword(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	wrong := postJSON(t, server, "/api/auth/2fa/setup", map[string]string{"password": "wrong password"})
	wrong.Body.Close()
	if wrong.StatusCode != http.StatusUnauthorized {
		t.Fatalf("setup with a wrong password status = %d, want %d", wrong.StatusCode, http.StatusUnauthorized)
	}

	enableTwoFactor(t, server)

	again := postJSON(t, server, "/api/auth/2fa/setup", map[string]string{"password": twoFactorTestPassword})
	again.Body.Close()
	if again.StatusCode != http.StatusConflict {
		t.Fatalf("setup while enabled status = %d, want %d", again.StatusCode, http.StatusConflict)
	}
}

func TestTwoFactorEnableRejectsAWrongCode(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	noPending := postJSON(t, server, "/api/auth/2fa/enable", map[string]string{"code": "123456"})
	noPending.Body.Close()
	if noPending.StatusCode != http.StatusConflict {
		t.Fatalf("enable without a setup status = %d, want %d", noPending.StatusCode, http.StatusConflict)
	}

	setup := postJSON(t, server, "/api/auth/2fa/setup", map[string]string{"password": twoFactorTestPassword})
	defer setup.Body.Close()
	if setup.StatusCode != http.StatusOK {
		t.Fatalf("2fa setup status = %d, want %d", setup.StatusCode, http.StatusOK)
	}
	var body struct {
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(setup.Body).Decode(&body); err != nil {
		t.Fatalf("decode setup body: %v", err)
	}

	wrongCode := codeAtTime(t, body.Secret, time.Now().Add(90*time.Second))
	enable := postJSON(t, server, "/api/auth/2fa/enable", map[string]string{"code": wrongCode})
	defer enable.Body.Close()
	if enable.StatusCode != http.StatusBadRequest {
		t.Fatalf("enable with a wrong code status = %d, want %d", enable.StatusCode, http.StatusBadRequest)
	}
	var enableBody map[string]string
	if err := json.NewDecoder(enable.Body).Decode(&enableBody); err != nil {
		t.Fatalf("decode enable body: %v", err)
	}
	if enableBody["error"] != "invalid code" {
		t.Fatalf("enable error = %q, want %q", enableBody["error"], "invalid code")
	}
}

func TestTwoFactorEnableAndDisableRevokeOtherSessions(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	sessionA := sessionToken(t, server)

	secondLogin := postJSON(t, server, "/api/auth/login", map[string]string{
		"email":    twoFactorTestEmail,
		"password": twoFactorTestPassword,
	})
	secondLogin.Body.Close()
	if secondLogin.StatusCode != http.StatusOK {
		t.Fatalf("second login status = %d, want %d", secondLogin.StatusCode, http.StatusOK)
	}
	sessionB := sessionToken(t, server)
	if sessionA == sessionB {
		t.Fatalf("the two logins share a session token")
	}

	secret := enableTwoFactor(t, server)

	if revoked := getWithSession(t, server, "/api/projects", sessionA); revoked.StatusCode != http.StatusUnauthorized {
		revoked.Body.Close()
		t.Fatalf("session A after enable status = %d, want %d", revoked.StatusCode, http.StatusUnauthorized)
	} else {
		revoked.Body.Close()
	}
	if kept := getWithSession(t, server, "/api/projects", sessionB); kept.StatusCode != http.StatusOK {
		kept.Body.Close()
		t.Fatalf("session B after enable status = %d, want %d", kept.StatusCode, http.StatusOK)
	} else {
		kept.Body.Close()
	}

	// A second code login cannot happen in the same 30-second step: a code is
	// single-use. Seed the other session directly so the disable path has one
	// session to revoke and one to keep.
	otherToken := "other-session-token"
	now := time.Now()
	if _, err := server.db.Exec(
		"INSERT INTO sessions (token, admin_id, expires_at, created_at) VALUES (?, 1, ?, ?)",
		otherToken, now.Add(time.Hour).Unix(), now.Unix(),
	); err != nil {
		t.Fatalf("insert other session: %v", err)
	}
	if live := getWithSession(t, server, "/api/projects", otherToken); live.StatusCode != http.StatusOK {
		live.Body.Close()
		t.Fatalf("seeded session status = %d, want %d", live.StatusCode, http.StatusOK)
	} else {
		live.Body.Close()
	}

	disable := postJSON(t, server, "/api/auth/2fa/disable", map[string]string{
		"password": twoFactorTestPassword,
		"code":     codeAtTime(t, secret, time.Now().Add(30*time.Second)),
	})
	disable.Body.Close()
	if disable.StatusCode != http.StatusNoContent {
		t.Fatalf("disable status = %d, want %d", disable.StatusCode, http.StatusNoContent)
	}

	if revoked := getWithSession(t, server, "/api/projects", otherToken); revoked.StatusCode != http.StatusUnauthorized {
		revoked.Body.Close()
		t.Fatalf("seeded session after disable status = %d, want %d", revoked.StatusCode, http.StatusUnauthorized)
	} else {
		revoked.Body.Close()
	}
	if kept := getWithSession(t, server, "/api/projects", sessionB); kept.StatusCode != http.StatusOK {
		kept.Body.Close()
		t.Fatalf("session B after disable status = %d, want %d", kept.StatusCode, http.StatusOK)
	} else {
		kept.Body.Close()
	}
}

func TestTwoFactorDisableNeedsPasswordAndCode(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	secret := enableTwoFactor(t, server)

	onlyPassword := postJSON(t, server, "/api/auth/2fa/disable", map[string]string{
		"password": twoFactorTestPassword,
	})
	onlyPassword.Body.Close()
	if onlyPassword.StatusCode != http.StatusUnauthorized {
		t.Fatalf("disable without a code status = %d, want %d", onlyPassword.StatusCode, http.StatusUnauthorized)
	}

	wrongCode := postJSON(t, server, "/api/auth/2fa/disable", map[string]string{
		"password": twoFactorTestPassword,
		"code":     "12345a",
	})
	wrongCode.Body.Close()
	if wrongCode.StatusCode != http.StatusUnauthorized {
		t.Fatalf("disable with a wrong code status = %d, want %d", wrongCode.StatusCode, http.StatusUnauthorized)
	}

	disable := postJSON(t, server, "/api/auth/2fa/disable", map[string]string{
		"password": twoFactorTestPassword,
		"code":     codeAtTime(t, secret, time.Now().Add(30*time.Second)),
	})
	disable.Body.Close()
	if disable.StatusCode != http.StatusNoContent {
		t.Fatalf("disable with password and code status = %d, want %d", disable.StatusCode, http.StatusNoContent)
	}

	login := postJSON(t, server, "/api/auth/login", map[string]string{
		"email":    twoFactorTestEmail,
		"password": twoFactorTestPassword,
	})
	defer login.Body.Close()
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login after disable status = %d, want %d", login.StatusCode, http.StatusOK)
	}
}

func TestReplayedCodeIsRefusedAtLogin(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	secret := enableTwoFactor(t, server)

	code := codeAtTime(t, secret, time.Now().Add(30*time.Second))
	first := loginWithCode(t, server, code)
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first login with code status = %d, want %d", first.StatusCode, http.StatusOK)
	}

	logout := server.request(t, http.MethodPost, "/api/auth/logout", nil)
	logout.Body.Close()

	replay := loginWithCode(t, server, code)
	defer replay.Body.Close()
	if replay.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed code status = %d, want %d", replay.StatusCode, http.StatusUnauthorized)
	}
	var body map[string]string
	if err := json.NewDecoder(replay.Body).Decode(&body); err != nil {
		t.Fatalf("decode replay body: %v", err)
	}
	if body["error"] != "invalid code" {
		t.Fatalf("replay error = %q, want %q", body["error"], "invalid code")
	}
}

func TestWrongCodesCountTowardTheLock(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	secret := enableTwoFactor(t, server)

	for attempt := 1; attempt <= 3; attempt++ {
		wrong := postJSON(t, server, "/api/auth/login", map[string]string{
			"email":    twoFactorTestEmail,
			"password": "wrong password",
		})
		wrong.Body.Close()
		if wrong.StatusCode != http.StatusUnauthorized {
			t.Fatalf("wrong-password attempt %d status = %d, want %d", attempt, wrong.StatusCode, http.StatusUnauthorized)
		}
	}

	wrongCode := codeAtTime(t, secret, time.Now().Add(90*time.Second))
	for attempt := 1; attempt <= 2; attempt++ {
		wrong := loginWithCode(t, server, wrongCode)
		wrong.Body.Close()
		if wrong.StatusCode != http.StatusUnauthorized {
			t.Fatalf("wrong-code attempt %d status = %d, want %d", attempt, wrong.StatusCode, http.StatusUnauthorized)
		}
	}

	locked := loginWithCode(t, server, codeAtTime(t, secret, time.Now().Add(30*time.Second)))
	defer locked.Body.Close()
	if locked.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("correct credentials while locked status = %d, want %d", locked.StatusCode, http.StatusTooManyRequests)
	}
	if retryAfter, err := strconv.Atoi(locked.Header.Get("Retry-After")); err != nil || retryAfter < 1 {
		t.Fatalf("Retry-After = %q, want a positive integer", locked.Header.Get("Retry-After"))
	}
}

func TestLoginAcceptsACodeWithSpaces(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	secret := enableTwoFactor(t, server)

	code := codeAtTime(t, secret, time.Now().Add(30*time.Second))
	spaced := code[:3] + " " + code[3:]
	response := loginWithCode(t, server, spaced)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("code with a space status = %d, want %d", response.StatusCode, http.StatusOK)
	}

	bad := loginWithCode(t, server, "12345a")
	bad.Body.Close()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("non-numeric code status = %d, want %d", bad.StatusCode, http.StatusUnauthorized)
	}
	if got := failedAttempts(t, server); got != 1 {
		t.Fatalf("failed_attempts after a non-numeric code = %d, want 1", got)
	}
}

func TestConcurrentLoginsWithOneCodeYieldOneSession(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	secret := enableTwoFactor(t, server)

	code := codeAtTime(t, secret, time.Now().Add(30*time.Second))
	const workers = 8

	statuses := make([]int, workers)
	var wait sync.WaitGroup
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			body, err := json.Marshal(map[string]string{
				"email":    twoFactorTestEmail,
				"password": twoFactorTestPassword,
				"code":     code,
			})
			if err != nil {
				statuses[i] = -1
				return
			}
			request := httptest.NewRequest(http.MethodPost, "http://relist-panel.test/api/auth/login", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			server.handler.ServeHTTP(recorder, request)
			statuses[i] = recorder.Code
		}(i)
	}
	wait.Wait()

	successes := 0
	for i, status := range statuses {
		switch status {
		case http.StatusOK:
			successes++
		case http.StatusUnauthorized, http.StatusTooManyRequests:
			// A replayed code is a credential failure; the failures trip the
			// account lock partway through, so the last requests are rate
			// limited instead of rejected. Only one session may exist.
		default:
			t.Fatalf("worker %d status = %d, want 200, 401 or 429 (%v)", i, status, statuses)
		}
	}
	if successes != 1 {
		t.Fatalf("successful logins = %d, want exactly 1 (%v)", successes, statuses)
	}

	var sessions int
	if err := server.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 2 {
		// The enabling session plus the one successful login.
		t.Fatalf("session rows = %d, want 2", sessions)
	}
}

func TestAccountWithoutTwoFactorIgnoresACode(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	response := loginWithCode(t, server, "000000")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login with a code while two-factor is off status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}
