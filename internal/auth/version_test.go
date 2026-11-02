package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"relist-panel/internal/database"
)

// The status route is the one public place the build version shows.
func TestStatusReportsTheBuildVersion(t *testing.T) {
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mux := http.NewServeMux()
	New(db).WithVersion("v1.2.3").RegisterRoutes(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/auth/status", nil))
	if body := strings.TrimSpace(recorder.Body.String()); recorder.Code != http.StatusOK || body != `{"authenticated":false,"version":"v1.2.3"}` {
		t.Fatalf("status = %d %s, want 200 with version v1.2.3", recorder.Code, body)
	}
}
