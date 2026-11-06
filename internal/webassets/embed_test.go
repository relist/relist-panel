package webassets

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandlerServesBuiltFilesAndShell(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":        {Data: []byte("<!doctype html><div id=root></div>")},
		"assets/index-a.js": {Data: []byte("console.log(1)")},
	}
	h := handler(dist)

	for path, want := range map[string]string{
		"/":                  "<div id=root>",
		"/assets/index-a.js": "console.log(1)",
		"/composes/7":        "<div id=root>",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("GET %s = %d %q, want 200 containing %q", path, rec.Code, rec.Body.String(), want)
		}
	}
}

// A checkout that never ran `task build-web` embeds only the placeholder, so
// the page says how to build it instead of a bare 404.
func TestHandlerSaysTheFrontendIsNotBuilt(t *testing.T) {
	h := handler(fstest.MapFS{".gitkeep": {Data: nil}})

	for _, path := range []string{"/", "/composes/7"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s = %d, want 503", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "task build-web") {
			t.Errorf("GET %s body = %q, want it to name task build-web", path, rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/nothing", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/nothing = %d, want 404", rec.Code)
	}
}
