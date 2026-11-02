package httpapi_test

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"relist-panel/internal/panelupdate"
)

// A panel that starts beside a run that is not over takes no writes, and the
// routes the update page and the health wait need stay open.
func TestStartsLockedBesideAnUnfinishedPanelUpdate(t *testing.T) {
	server := newTestServer(t)
	dir := panelupdate.RunDir(server.dataDir, "20261101T120000Z")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	st := panelupdate.RunStatus{Run: "20261101T120000Z", State: panelupdate.StateWaitingHealth, FromVersion: "1.3.0", ToVersion: "1.4.0"}
	if err := panelupdate.WriteStatus(dir, st, time.Now()); err != nil {
		t.Fatal(err)
	}
	server.useRunner(t, newFakeComposeRunner())
	setupAndLogin(t, server) // signing in stays open while locked

	response := server.request(t, http.MethodPost, "/api/projects", map[string]any{"name": "blocked"})
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("write while updating = %d, want 503", response.StatusCode)
	}
	response = server.request(t, http.MethodGet, "/healthz", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("/healthz = %d", response.StatusCode)
	}
	response = server.request(t, http.MethodGet, "/api/panel-update/status", nil)
	defer response.Body.Close()
	var status panelupdate.Status
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&status) != nil {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if !status.Locked || status.Run == nil || status.Run.State != panelupdate.StateWaitingHealth {
		t.Fatalf("status = %+v run = %+v", status, status.Run)
	}
}
