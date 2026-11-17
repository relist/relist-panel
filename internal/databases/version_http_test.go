package databases

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestChangeVersionAPI(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "16")
	w := a.do("POST", "/api/databases/1/change-version", `{"version":"18","backupFirst":true}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("POST = %d %s", w.Code, w.Body)
	}
	var row VersionChangeJSON
	if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil || row.ID != 1 || row.Status != JobRunning ||
		row.FromVersion != "16" || row.ToVersion != "18" || row.Method != VersionMethodDumpRestore {
		t.Fatalf("row = %+v, %v (%s)", row, err, w.Body)
	}
	w = a.do("POST", "/api/databases/1/change-version", `{"version":"16"}`)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "pick a version higher") {
		t.Fatalf("not higher = %d %s", w.Code, w.Body)
	}
	if w := a.do("POST", "/api/databases/1/change-version", `{"version":"99"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown tag = %d %s", w.Code, w.Body)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.events {
		if e.Target != "Shop" {
			t.Fatalf("event without target: %+v", e)
		}
	}
}

func listChanges(t *testing.T, a *apiRig) []VersionChangeJSON {
	t.Helper()
	w := a.do("GET", "/api/databases/1/version-changes", "")
	var out []VersionChangeJSON
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("GET = %d %s", w.Code, w.Body)
	}
	return out
}

func TestVersionChangesList(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "16")
	if len(listChanges(t, a)) != 0 {
		t.Fatal("not empty before a change")
	}
	a.do("POST", "/api/databases/1/change-version", `{"version":"18"}`)
	list := listChanges(t, a)
	if len(list) != 1 || list[0].Status != JobSucceeded || list[0].PreviousData != "relist-db-1" || list[0].FinishedAt == nil || list[0].Log == "" {
		t.Fatalf("list = %+v", list)
	}
}

func TestDeletePreviousData(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "16")
	a.do("POST", "/api/databases/1/change-version", `{"version":"18"}`)
	if w := a.do("DELETE", "/api/databases/1/version-changes/1/previous-data", ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", w.Code, w.Body)
	}
	if !a.host.saw("remove-volume relist-db-1") {
		t.Fatalf("calls = %v", a.host.all())
	}
	if list := listChanges(t, a); list[0].PreviousData != "" {
		t.Fatalf("previousData = %q", list[0].PreviousData)
	}
	if w := a.do("DELETE", "/api/databases/1/version-changes/1/previous-data", ""); w.Code != http.StatusNotFound {
		t.Fatalf("second DELETE = %d %s", w.Code, w.Body)
	}
}
