package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"relist-panel/internal/database"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func record(t *testing.T, db *sql.DB, e Event) {
	t.Helper()
	if e.At.IsZero() {
		e.At = time.UnixMilli(1000)
	}
	if e.Outcome == "" {
		e.Outcome = OutcomeOK
	}
	if e.Actor == "" {
		e.Actor = "a@example.com"
	}
	if err := Record(context.Background(), db, e); err != nil {
		t.Fatal(err)
	}
}

func list(t *testing.T, db *sql.DB, f Filter) ([]Row, int64) {
	t.Helper()
	rows, next, err := List(context.Background(), db, f)
	if err != nil {
		t.Fatal(err)
	}
	return rows, next
}

func actions(rows []Row) []string {
	out := []string{}
	for _, row := range rows {
		out = append(out, row.Action)
	}
	return out
}

func TestRecordAndList(t *testing.T) {
	db := openDB(t)
	record(t, db, Event{Action: "project.create", Target: "alpha"})
	record(t, db, Event{Action: "compose.update", ActorUserID: 2, ProjectID: 1, EnvironmentID: 11, ComposeID: 101,
		Target: "alpha / production / web", Detail: map[string]any{"key": "TOKEN"}, IP: "192.0.2.1"})
	record(t, db, Event{Action: "auth.login_failed", Outcome: OutcomeDenied, Actor: "unknown"})
	rows, next := list(t, db, Filter{})
	if next != 0 || len(rows) != 3 {
		t.Fatalf("rows = %d next %d", len(rows), next)
	}
	if got := actions(rows); got[0] != "auth.login_failed" || got[2] != "project.create" {
		t.Fatalf("order = %v, want newest first", got)
	}
	update := rows[1]
	if update.At != 1000 || update.Detail["key"] != "TOKEN" || *update.ProjectID != 1 || *update.ComposeID != 101 || *update.IP != "192.0.2.1" {
		t.Fatalf("update row = %+v", update)
	}
	body, _ := json.Marshal(rows[2])
	for _, want := range []string{`"actorUserId":null`, `"projectId":null`, `"detail":null`, `"ip":null`} {
		if !json.Valid(body) || !strings.Contains(string(body), want) {
			t.Fatalf("project.create JSON %s lacks %s", body, want)
		}
	}
}

func TestListPagesByCursor(t *testing.T) {
	db := openDB(t)
	for i := 0; i < 150; i++ {
		record(t, db, Event{Action: "compose.update"})
	}
	first, next := list(t, db, Filter{})
	if len(first) != 100 || next == 0 {
		t.Fatalf("page 1 = %d next %d", len(first), next)
	}
	second, last := list(t, db, Filter{Cursor: next})
	if len(second) != 50 || last != 0 {
		t.Fatalf("page 2 = %d next %d", len(second), last)
	}
	seen := map[int64]bool{}
	for _, row := range append(first, second...) {
		seen[row.ID] = true
	}
	if len(seen) != 150 || first[0].ID < first[99].ID || first[99].ID < second[0].ID {
		t.Fatalf("distinct = %d, not newest first", len(seen))
	}
}

func TestListFilters(t *testing.T) {
	db := openDB(t)
	record(t, db, Event{Action: "compose.update", Actor: "Bob@Example.com", At: time.UnixMilli(100), ProjectID: 1, EnvironmentID: 11})
	record(t, db, Event{Action: "compose_variable.create", At: time.UnixMilli(200), ProjectID: 1, EnvironmentID: 12})
	record(t, db, Event{Action: "project.update", At: time.UnixMilli(300), ProjectID: 1})
	record(t, db, Event{Action: "compose.stop", At: time.UnixMilli(400), ProjectID: 2, EnvironmentID: 21})
	record(t, db, Event{Action: "cli.reset_2fa", Actor: ActorCLI, At: time.UnixMilli(500)})

	if rows, _ := list(t, db, Filter{Actor: "bob@example.com"}); len(rows) != 1 || rows[0].Action != "compose.update" {
		t.Fatalf("actor = %v", actions(rows))
	}
	if rows, _ := list(t, db, Filter{Actor: "cli"}); len(rows) != 1 {
		t.Fatalf("cli = %v", actions(rows))
	}
	if rows, _ := list(t, db, Filter{Action: "compose."}); len(rows) != 2 {
		t.Fatalf("action prefix = %v", actions(rows))
	}
	if rows, _ := list(t, db, Filter{Action: "compose_"}); len(rows) != 1 {
		t.Fatalf("underscore is literal = %v", actions(rows))
	}
	if rows, _ := list(t, db, Filter{From: 200, To: 400}); len(rows) != 3 {
		t.Fatalf("from/to inclusive = %v", actions(rows))
	}
	if rows, _ := list(t, db, Filter{ProjectID: 1}); len(rows) != 3 {
		t.Fatalf("project = %v", actions(rows))
	}
	rows, _ := list(t, db, Filter{ProjectID: 1, EnvironmentIDs: []int64{12}, ProjectOwn: true})
	if got := actions(rows); len(got) != 2 || got[0] != "project.update" || got[1] != "compose_variable.create" {
		t.Fatalf("edited environments + project own = %v", got)
	}
	if rows, _ := list(t, db, Filter{ProjectID: 1, EnvironmentIDs: []int64{}, ProjectOwn: false}); len(rows) != 0 {
		t.Fatalf("no environments = %v", actions(rows))
	}
}

func TestPruneKeepsRecentAndForever(t *testing.T) {
	db := openDB(t)
	now := time.UnixMilli(10 * 24 * 3600 * 1000)
	record(t, db, Event{Action: "old", At: now.Add(-48 * time.Hour)})
	record(t, db, Event{Action: "fresh", At: now.Add(-time.Hour)})
	if err := SetSettings(context.Background(), db, 0); err != nil {
		t.Fatal(err)
	}
	if n, err := Prune(context.Background(), db, now); err != nil || n != 0 {
		t.Fatalf("forever pruned %d, %v", n, err)
	}
	if err := SetSettings(context.Background(), db, 1); err != nil {
		t.Fatal(err)
	}
	if days, err := Settings(context.Background(), db); err != nil || days != 1 {
		t.Fatalf("settings = %d, %v", days, err)
	}
	if n, err := Prune(context.Background(), db, now); err != nil || n != 1 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	if rows, _ := list(t, db, Filter{}); len(rows) != 1 || rows[0].Action != "fresh" {
		t.Fatalf("left = %v", actions(rows))
	}
	if err := SetSettings(context.Background(), db, -1); err != ErrInvalidRetention {
		t.Fatalf("negative = %v", err)
	}
}
