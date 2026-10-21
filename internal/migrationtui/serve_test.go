package migrationtui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"relist-panel/internal/migration"
	"relist-panel/internal/migrationctl"
)

// sendAll is send that follows the commands a few levels and reports
// whether the model asked to quit.
func sendAll(t *testing.T, m tea.Model, msg tea.Msg) (tea.Model, bool) {
	t.Helper()
	quit := false
	m, cmd := m.Update(msg)
	for range 3 {
		if cmd == nil {
			break
		}
		outs := runCmd(cmd)
		cmd = nil
		for _, out := range outs {
			switch out.(type) {
			case tea.QuitMsg:
				quit = true
			case ActionDoneMsg, tokenMsg, statusMsg, resumeDoneMsg:
				var next tea.Cmd
				m, next = m.Update(out)
				if next != nil {
					cmd = next
				}
			}
		}
	}
	return m, quit
}

func serveWith(c *fakeController, s migration.SourceStatus, a migration.Activity) tea.Model {
	m := NewServe(c)
	m, _ = m.Update(statusMsg{S: s, A: a})
	return m
}

func TestServeOffersLifetimes(t *testing.T) {
	c := &fakeController{}
	m := serveWith(c, migration.SourceStatus{}, migration.Activity{})
	mustContain(t, m, "1 hour", "> 24 hours", "7 days")
	m = send(t, m, key("enter"))
	if len(c.tokens) != 1 || c.tokens[0] != "24h" {
		t.Fatalf("CreateToken calls = %q, want 24h", c.tokens)
	}
}

func TestServeWarnsALiveTokenIsRevoked(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	m := serveWith(&fakeController{}, migration.SourceStatus{TokenExpiresAt: &expires}, migration.Activity{})
	mustContain(t, m, "Creating a token revokes the one that is live.")
}

func TestServeShowsTheTokenOnce(t *testing.T) {
	expires := time.Date(2026, 10, 21, 9, 30, 0, 0, time.UTC)
	c := &fakeController{createdTok: migrationctl.Token{Token: "tok-123", ExpiresAt: expires}}
	m := serveWith(c, migration.SourceStatus{}, migration.Activity{})
	m = send(t, m, key("enter"))
	mustContain(t, m, "tok-123", "relist-panel migration pull", "Waiting for the new server to connect")
	if !strings.Contains(screen(m), expires.Local().Format("2006-01-02 15:04")) {
		t.Fatalf("expiry not shown:\n%s", screen(m))
	}
}

func TestServeFollowsTheLock(t *testing.T) {
	m := serveWith(&fakeController{}, migration.SourceStatus{Step: migration.StepReady, ClaimedBy: "203.0.113.9"},
		migration.Activity{Item: "volume-a", Bytes: 2048, Done: 3})
	mustContain(t, m, "Claimed by 203.0.113.9", "✓ Lock this server", "✓ Stop its Composes", "✓ Start the maintenance page",
		"Sending volume-a (2.0 KiB)", "3 items sent")
	m, _ = m.Update(statusMsg{S: migration.SourceStatus{Step: migration.StepStopping, ClaimedBy: "x"}})
	mustContain(t, m, "✓ Lock this server", "▸ Stop its Composes")
}

func TestServeShowsMigrated(t *testing.T) {
	at := time.Now()
	m := serveWith(&fakeController{}, migration.SourceStatus{Step: migration.StepMigrated, TargetURL: "https://new.example.com", MigratedAt: &at}, migration.Activity{})
	mustContain(t, m, "Migrated to https://new.example.com")
}

func TestServeQuitBeforeClaimRevokes(t *testing.T) {
	c := &fakeController{createdTok: migrationctl.Token{Token: "tok"}}
	m := serveWith(c, migration.SourceStatus{}, migration.Activity{})
	m = send(t, m, key("enter"))
	m, quit := sendAll(t, m, key("q"))
	if quit {
		t.Fatal("quit without asking about the token")
	}
	mustContain(t, m, "Revoke the token? [Y/n]")
	if _, quit = sendAll(t, m, key("enter")); !quit || c.revoked != 1 {
		t.Fatalf("enter: quit=%v revoked=%d, want quit after one revoke", quit, c.revoked)
	}

	c2 := &fakeController{createdTok: migrationctl.Token{Token: "tok"}}
	m = serveWith(c2, migration.SourceStatus{}, migration.Activity{})
	m = send(t, m, key("enter"))
	m, _ = sendAll(t, m, key("q"))
	if _, quit = sendAll(t, m, key("n")); !quit || c2.revoked != 0 {
		t.Fatalf("n: quit=%v revoked=%d, want quit without a revoke", quit, c2.revoked)
	}
}

func TestServeCancelNeedsRESUME(t *testing.T) {
	c := &fakeController{}
	m := serveWith(c, migration.SourceStatus{Step: migration.StepReady, ClaimedBy: "x"}, migration.Activity{})
	m = send(t, m, key("c"))
	mustContain(t, m, "RESUME")
	m = typeText(t, m, "RESUM")
	m = send(t, m, key("enter"))
	if c.resumed != 0 {
		t.Fatal("resumed without RESUME typed in full")
	}
	mustContain(t, m, "type RESUME to confirm")
	m = typeText(t, m, "E")
	m = send(t, m, key("enter"))
	if c.resumed != 1 {
		t.Fatalf("resume calls = %d", c.resumed)
	}
}

func TestServeSkipsTheTokenWhenUnderWay(t *testing.T) {
	m := serveWith(&fakeController{}, migration.SourceStatus{Step: migration.StepStopping, ClaimedBy: "x"}, migration.Activity{})
	if strings.Contains(screen(m), "24 hours") {
		t.Fatal("offered a token while a migration is under way")
	}
	mustContain(t, m, "Claimed by x")
}

func TestResumeNeedsRESUME(t *testing.T) {
	c := &fakeController{resumeWarn: "the maintenance container was already gone"}
	m := NewResume(c)
	m, _ = m.Update(statusMsg{S: migration.SourceStatus{Step: migration.StepMigrated, TargetURL: "https://new.example.com"}})
	mustContain(t, m, "Migrated to https://new.example.com", "RESUME")
	m = typeText(t, m, "resume")
	m = send(t, m, key("enter"))
	if c.resumed != 0 {
		t.Fatal("resumed on lower-case resume")
	}
	m = send(t, m, key("ctrl+u"))
	m = typeText(t, m, "RESUME")
	m = send(t, m, key("enter"))
	if c.resumed != 1 {
		t.Fatalf("resume calls = %d", c.resumed)
	}
	mustContain(t, m, "the maintenance container was already gone")
}

func TestResumeWithNothingLocked(t *testing.T) {
	m := NewResume(&fakeController{})
	m, _ = m.Update(statusMsg{S: migration.SourceStatus{}})
	mustContain(t, m, "This server is not locked for a migration.")
}

func TestServeFollowsAClaimBeforeStart(t *testing.T) {
	c := &fakeController{}
	m := serveWith(c, migration.SourceStatus{ClaimedBy: "203.0.113.9"}, migration.Activity{})
	if strings.Contains(screen(m), "24 hours") {
		t.Fatal("offered a new token while the new server holds the claimed one")
	}
	mustContain(t, m, "Claimed by 203.0.113.9")
	m = send(t, m, key("enter"))
	if len(c.tokens) != 0 {
		t.Fatalf("created a token over a claimed session: %q", c.tokens)
	}
}

func TestServeCtrlCBeforeClaimAsksToo(t *testing.T) {
	c := &fakeController{createdTok: migrationctl.Token{Token: "tok"}}
	m := serveWith(c, migration.SourceStatus{}, migration.Activity{})
	m = send(t, m, key("enter"))
	m, quit := sendAll(t, m, key("ctrl+c"))
	if quit {
		t.Fatal("ctrl+c quit without asking about the live token")
	}
	mustContain(t, m, "Revoke the token? [Y/n]")
	if _, quit = sendAll(t, m, key("ctrl+c")); !quit || c.revoked != 0 {
		t.Fatalf("second ctrl+c: quit=%v revoked=%d, want quit without a revoke", quit, c.revoked)
	}
}

func TestServeQuitNote(t *testing.T) {
	expires := time.Date(2026, 10, 21, 9, 30, 0, 0, time.UTC)
	c := &fakeController{createdTok: migrationctl.Token{Token: "tok", ExpiresAt: expires}}
	m := serveWith(c, migration.SourceStatus{}, migration.Activity{})
	if note := QuitNote(m); note != "" {
		t.Fatalf("note before a token = %q", note)
	}
	m = send(t, m, key("enter"))
	if note := QuitNote(m); !strings.Contains(note, "live until "+expires.Local().Format("2006-01-02 15:04")) {
		t.Fatalf("note with a live token = %q", note)
	}
	m, _ = m.Update(statusMsg{S: migration.SourceStatus{Step: migration.StepReady, ClaimedBy: "x"}})
	if note := QuitNote(m); !strings.Contains(note, "relist-panel migration serve") {
		t.Fatalf("note under way = %q", note)
	}
}

func TestServeResumeDoesNotStartASecondPoll(t *testing.T) {
	m := serveWith(&fakeController{}, migration.SourceStatus{Step: migration.StepReady, ClaimedBy: "x"}, migration.Activity{})
	if _, cmd := m.Update(resumeDoneMsg{}); cmd != nil {
		t.Fatal("a resume started another status poll; the running one picks the change up")
	}
}

// A claimed session that never started must be endable from the old host:
// otherwise the new server can lock it whenever it likes.
func TestServeCancelsAClaimBeforeStart(t *testing.T) {
	c := &fakeController{}
	m := serveWith(c, migration.SourceStatus{ClaimedBy: "203.0.113.9"}, migration.Activity{})
	mustContain(t, m, "c cancel the migration")
	m = send(t, m, key("c"))
	m = typeText(t, m, "RESUME")
	m = send(t, m, key("enter"))
	if c.resumed != 1 {
		t.Fatalf("resume calls = %d, want the claimed session ended", c.resumed)
	}
}

func TestResumeEndsAClaimBeforeStart(t *testing.T) {
	c := &fakeController{}
	m := NewResume(c)
	m, _ = m.Update(statusMsg{S: migration.SourceStatus{ClaimedBy: "203.0.113.9"}})
	mustContain(t, m, "Claimed by 203.0.113.9", "RESUME")
	m = typeText(t, m, "RESUME")
	m = send(t, m, key("enter"))
	if c.resumed != 1 {
		t.Fatalf("resume calls = %d, want the claimed session ended", c.resumed)
	}
}

func TestMigratedToNamesTheTargetAndWhen(t *testing.T) {
	at := time.Date(2026, 10, 21, 9, 30, 0, 0, time.Local)
	if got := MigratedTo(migration.SourceStatus{TargetURL: "https://new.example.com", MigratedAt: &at}); got != "Migrated to https://new.example.com on 2026-10-21 09:30" {
		t.Fatalf("MigratedTo = %q", got)
	}
	if got := MigratedTo(migration.SourceStatus{TargetURL: "https://new.example.com"}); got != "Migrated to https://new.example.com" {
		t.Fatalf("MigratedTo without a time = %q", got)
	}
}
