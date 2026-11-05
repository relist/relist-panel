package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type loginEvent struct {
	action, outcome, actor string
	actorID                *int64
	detail, ip             string
}

func loginEvents(t *testing.T, f *userFixture) []loginEvent {
	t.Helper()
	rows, err := f.db.Query("SELECT action, outcome, actor, actor_user_id, COALESCE(detail, ''), COALESCE(ip, '') FROM audit_events ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []loginEvent
	for rows.Next() {
		var e loginEvent
		if err := rows.Scan(&e.action, &e.outcome, &e.actor, &e.actorID, &e.detail, &e.ip); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func TestLoginIsRecorded(t *testing.T) {
	f := newUserFixture(t)
	if response := f.login("admin@example.com", userPassword); response.Code != http.StatusOK {
		t.Fatalf("login = %d", response.Code)
	}
	if response := f.login("Admin@Example.com", "wrong password typed"); response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d", response.Code)
	}
	if response := f.login("nobody@example.com", "wrong password typed"); response.Code != http.StatusUnauthorized {
		t.Fatalf("unknown = %d", response.Code)
	}
	events := loginEvents(t, f)
	if len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
	ok, wrong, unknown := events[0], events[1], events[2]
	if ok.action != "auth.login" || ok.outcome != "ok" || ok.actor != "admin@example.com" || ok.actorID == nil || *ok.actorID != 1 || ok.ip != "192.0.2.1" {
		t.Errorf("login = %+v", ok)
	}
	var detail map[string]any
	_ = json.Unmarshal([]byte(wrong.detail), &detail)
	if wrong.action != "auth.login_failed" || wrong.outcome != "denied" || wrong.actor != "admin@example.com" || detail["email"] != "admin@example.com" {
		t.Errorf("wrong password = %+v", wrong)
	}
	_ = json.Unmarshal([]byte(unknown.detail), &detail)
	if unknown.action != "auth.login_failed" || unknown.actor != "unknown" || unknown.actorID != nil || detail["email"] != "nobody@example.com" {
		t.Errorf("unknown email = %+v", unknown)
	}
	for _, e := range events {
		if strings.Contains(e.detail, "wrong password typed") || strings.Contains(e.detail, userPassword) {
			t.Errorf("a password reached the audit log: %+v", e)
		}
	}
}

func TestInviteAcceptIsRecorded(t *testing.T) {
	f := newUserFixture(t)
	token := f.invite(t)
	if response := f.do(http.MethodPost, "/api/invites/"+token, "", map[string]string{"password": newPassword}); response.Code != http.StatusOK {
		t.Fatalf("accept = %d", response.Code)
	}
	var found bool
	for _, e := range loginEvents(t, f) {
		if e.action == "auth.invite_accepted" {
			found = e.outcome == "ok" && e.actor == "bob@example.com" && e.actorID != nil && *e.actorID == 2
		}
	}
	if !found {
		t.Fatalf("events = %+v", loginEvents(t, f))
	}
}

// A script guessing passwords from one address records at most
// failedLoginsPerMinute failed sign-ins a minute; a success still records.
func TestFailedLoginsAreCappedPerAddress(t *testing.T) {
	f := newUserFixture(t)
	for range failedLoginsPerMinute + 3 {
		if response := f.login("nobody@example.com", "guess"); response.Code != http.StatusUnauthorized {
			t.Fatalf("unknown = %d", response.Code)
		}
	}
	if response := f.login("admin@example.com", userPassword); response.Code != http.StatusOK {
		t.Fatalf("login = %d", response.Code)
	}
	events := loginEvents(t, f)
	if len(events) != failedLoginsPerMinute+1 || events[len(events)-1].action != "auth.login" {
		t.Fatalf("events = %d, want %d failed and the login", len(events), failedLoginsPerMinute)
	}
}

// Behind Traefik every sign-in comes from one address: one script's tries
// must not use up the cap for another account's failures.
func TestFailedLoginCapIsPerEmail(t *testing.T) {
	f := newUserFixture(t)
	for range failedLoginsPerMinute + 3 {
		f.login("nobody@example.com", "guess")
	}
	if response := f.login("Admin@Example.com", "wrong password typed"); response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d", response.Code)
	}
	events := loginEvents(t, f)
	if last := events[len(events)-1]; last.actor != "admin@example.com" {
		t.Fatalf("the admin's failure was dropped: last event %+v", last)
	}
}

// Emails with no account share one cap per address: typing a new email on
// every try must not get past it.
func TestFailedLoginsWithUnknownEmailsShareOneCap(t *testing.T) {
	f := newUserFixture(t)
	for i := range failedLoginsPerMinute + 3 {
		f.login(fmt.Sprintf("nobody%d@example.com", i), "guess")
	}
	f.login("admin@example.com", "wrong password typed")
	events := loginEvents(t, f)
	if len(events) != failedLoginsPerMinute+1 || events[len(events)-1].actor != "admin@example.com" {
		t.Fatalf("events = %d, want %d unknown and the admin's failure", len(events), failedLoginsPerMinute)
	}
}
