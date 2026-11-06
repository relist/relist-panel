package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"
)

func requestsPath(p projectResponse, e environmentResponse, c composeResponse) string {
	return fmt.Sprintf("/api/projects/%d/environments/%d/composes/%d/requests/metrics?window=1h", p.ID, e.ID, c.ID)
}

// A Project Viewer reads their Compose's request metrics; other Projects'
// Composes, a Compose under the wrong Project path, and the host-wide views
// stay refused.
func TestRequestMetricsAccess(t *testing.T) {
	f := newRoleFixture(t)
	f.grant(t, f.alpha.ID, 0, "viewer")

	if code, body := f.get(t, requestsPath(f.alpha, f.alphaProd, f.alphaWeb)); code != http.StatusOK {
		t.Fatalf("viewer on own compose = %d %s", code, body)
	}
	if code, _ := f.get(t, requestsPath(f.beta, f.betaE, f.betaWeb)); code != http.StatusNotFound && code != http.StatusForbidden {
		t.Fatalf("other project's compose = %d, want a refusal", code)
	}
	if code, _ := f.get(t, requestsPath(f.alpha, f.alphaProd, f.betaWeb)); code != http.StatusNotFound {
		t.Fatalf("another project's compose under this project's path = %d, want 404", code)
	}
	for _, path := range []string{"/api/requests/metrics?window=1h", "/api/requests/panel/metrics?window=1h", "/api/requests/settings"} {
		if code, _ := f.get(t, path); code != http.StatusForbidden && code != http.StatusNotFound {
			t.Errorf("member GET %s = %d, want a refusal", path, code)
		}
	}
	response := f.server.request(t, http.MethodPut, "/api/requests/settings", map[string]int{"retentionDays": 7})
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden && response.StatusCode != http.StatusNotFound {
		t.Errorf("member PUT settings = %d, want a refusal", response.StatusCode)
	}

	signInAsUser(t, f.server, 3, "admin2@example.com", true)
	for _, path := range []string{"/api/requests/metrics?window=1h", "/api/requests/panel/metrics?window=1h", "/api/requests/settings"} {
		if code, body := f.get(t, path); code != http.StatusOK {
			t.Errorf("admin GET %s = %d %s", path, code, body)
		}
	}
}

func TestRequestMetricsSettingsAreAudited(t *testing.T) {
	f := newRoleFixture(t)
	signInAsUser(t, f.server, 3, "admin2@example.com", true)
	response := f.server.request(t, http.MethodPut, "/api/requests/settings", map[string]int{"retentionDays": 30})
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d", response.StatusCode)
	}
	rows := auditActions(auditRows(t, f.server), "request_metrics.settings_update")
	if len(rows) != 1 || rows[0].Outcome != "ok" || rows[0].Detail["retentionDays"] != float64(30) {
		t.Fatalf("audit rows = %+v", rows)
	}
	if code, body := f.get(t, "/api/requests/metrics?window=30d"); code != http.StatusOK {
		t.Fatalf("30d after raising retention = %d %s", code, body)
	}
}
