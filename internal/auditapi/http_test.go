package auditapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"relist-panel/internal/audit"
	"relist-panel/internal/database"
)

func setup(t *testing.T) (*sql.DB, http.Handler, *Service) {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	service := New(db)
	mux := http.NewServeMux()
	service.RegisterRoutes(mux)
	return db, mux, service
}

func call(t *testing.T, handler http.Handler, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, path, reader))
	var out map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &out)
	return recorder.Code, out
}

func TestAuditListFiltersAndPages(t *testing.T) {
	db, handler, _ := setup(t)
	for i := 0; i < 105; i++ {
		action := "compose.update"
		if i%2 == 0 {
			action = "project.update"
		}
		if err := audit.Record(context.Background(), db, audit.Event{At: time.UnixMilli(int64(1000 + i)), Actor: "a@example.com", Action: action, Outcome: audit.OutcomeOK}); err != nil {
			t.Fatal(err)
		}
	}
	status, page := call(t, handler, http.MethodGet, "/api/audit", nil)
	items, _ := page["items"].([]any)
	if status != http.StatusOK || len(items) != 100 || page["nextCursor"] == nil {
		t.Fatalf("page 1 = %d, %d items, next %v", status, len(items), page["nextCursor"])
	}
	cursor := int64(page["nextCursor"].(float64))
	status, page = call(t, handler, http.MethodGet, "/api/audit?cursor="+itoa(cursor), nil)
	if items, _ := page["items"].([]any); status != http.StatusOK || len(items) != 5 || page["nextCursor"] != nil {
		t.Fatalf("page 2 = %d %v", status, page)
	}
	_, page = call(t, handler, http.MethodGet, "/api/audit?action=compose.&actor=A@example.com&from=1000&to=1009", nil)
	if items, _ := page["items"].([]any); len(items) != 5 {
		t.Fatalf("filtered = %v", page)
	}
	for _, bad := range []string{"cursor=x", "from=-1", "to=soon"} {
		if status, body := call(t, handler, http.MethodGet, "/api/audit?"+bad, nil); status != http.StatusBadRequest || body["field"] == "" {
			t.Errorf("%s = %d %v", bad, status, body)
		}
	}
}

func itoa(n int64) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

func TestAuditSettingsRoundTrip(t *testing.T) {
	_, handler, _ := setup(t)
	if status, body := call(t, handler, http.MethodGet, "/api/audit/settings", nil); status != http.StatusOK || body["retentionDays"] != float64(90) || body["keepForever"] != false {
		t.Fatalf("default = %d %v", status, body)
	}
	if status, body := call(t, handler, http.MethodPut, "/api/audit/settings", map[string]any{"retentionDays": 30, "keepForever": false}); status != http.StatusOK || body["retentionDays"] != float64(30) {
		t.Fatalf("30 days = %d %v", status, body)
	}
	if status, body := call(t, handler, http.MethodPut, "/api/audit/settings", map[string]any{"retentionDays": 30, "keepForever": true}); status != http.StatusOK || body["keepForever"] != true || body["retentionDays"] != float64(90) {
		t.Fatalf("forever = %d %v", status, body)
	}
	if status, body := call(t, handler, http.MethodPut, "/api/audit/settings", map[string]any{"retentionDays": 0, "keepForever": false}); status != http.StatusBadRequest || body["field"] != "retentionDays" {
		t.Fatalf("0 days = %d %v", status, body)
	}
}

func TestRetentionRunsOnStart(t *testing.T) {
	db, _, service := setup(t)
	now := time.Now()
	service.now = func() time.Time { return now }
	if err := audit.Record(context.Background(), db, audit.Event{At: now.Add(-100 * 24 * time.Hour), Actor: "a", Action: "x.y", Outcome: audit.OutcomeOK}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		service.RunRetention(ctx)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var n int
		_ = db.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&n)
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the old event was not pruned on start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestAuditSettingsRefuseAHugeRetention(t *testing.T) {
	_, handler, _ := setup(t)
	if status, body := call(t, handler, http.MethodPut, "/api/audit/settings", map[string]any{"retentionDays": 999999, "keepForever": false}); status != http.StatusBadRequest || body["field"] != "retentionDays" {
		t.Fatalf("999999 days = %d %v", status, body)
	}
}
