package httpapi_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The session cookie is SameSite=Lax, which a sibling subdomain counts as the
// same site: a page on app.example.com could post to panel.example.com with
// the admin's cookie. Writes from any other origin are refused.
func TestWritesFromAnotherOriginAreRefused(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{"sibling subdomain", map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "http://app.relist-panel.test"}},
		{"another site", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "http://evil.example"}},
		{"old browser, other origin", map[string]string{"Origin": "http://app.relist-panel.test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://relist-panel.test/api/projects", bytes.NewReader([]byte(`{"name":"Forged"}`)))
			request.Header.Set("Content-Type", "text/plain")
			for k, v := range tc.headers {
				request.Header.Set(k, v)
			}
			response := server.serve(request)
			response.Body.Close()
			if response.StatusCode != http.StatusForbidden {
				t.Fatalf("POST from %s = %d, want 403", tc.name, response.StatusCode)
			}
		})
	}

	list := server.request(t, http.MethodGet, "/api/projects", nil)
	if body := readBody(t, list); bytes.Contains([]byte(body), []byte("Forged")) {
		t.Fatalf("a refused write still created the Project: %s", body)
	}
}

// The web app's own requests, and clients that send no browser headers at
// all (the migration pull, curl), keep working.
func TestWritesFromThePanelAndNonBrowserClientsPass(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	for i, headers := range []map[string]string{
		{"Sec-Fetch-Site": "same-origin", "Origin": "http://relist-panel.test"},
		{"Origin": "http://relist-panel.test"},
		{},
	} {
		name := []string{"Same", "Origin", "None"}[i]
		request := httptest.NewRequest(http.MethodPost, "http://relist-panel.test/api/projects", bytes.NewReader([]byte(`{"name":"`+name+`"}`)))
		request.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			request.Header.Set(k, v)
		}
		response := server.serve(request)
		if body := readBody(t, response); response.StatusCode != http.StatusCreated {
			t.Fatalf("POST with %v = %d %s, want 201", headers, response.StatusCode, body)
		}
	}
}

// Reads stay open to any origin's navigation, as SameSite=Lax already allows.
func TestReadsFromAnotherOriginPass(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	request := httptest.NewRequest(http.MethodGet, "http://relist-panel.test/api/projects", nil)
	request.Header.Set("Sec-Fetch-Site", "same-site")
	request.Header.Set("Origin", "http://app.relist-panel.test")
	response := server.serve(request)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET from a sibling subdomain = %d, want 200", response.StatusCode)
	}
}
