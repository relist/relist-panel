package builders

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relist-panel/internal/audit"
)

func TestCreateNamesTheBuilderInTheAuditEvent(t *testing.T) {
	f := newServiceFixture(t)
	mux := http.NewServeMux()
	f.svc.RegisterRoutes(mux)
	ctx, pending := audit.Begin(context.Background(), audit.Event{}, nil, nil)
	body := `{"name":"small","cpus":0.5,"memoryMb":1024,"maxParallelism":null}`
	req := httptest.NewRequest(http.MethodPost, "/api/builders", strings.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if got := pending.Event().Target; got != "small" {
		t.Fatalf("target = %q, want the builder's name", got)
	}
}
