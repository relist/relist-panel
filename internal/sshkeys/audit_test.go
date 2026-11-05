package sshkeys

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relist-panel/internal/audit"
)

func TestCreateNamesTheKeyInTheAuditEvent(t *testing.T) {
	mux := newMux(t)
	ctx, pending := audit.Begin(context.Background(), audit.Event{}, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/ssh-keys", strings.NewReader(`{"name":"ci"}`)).WithContext(ctx)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if got := pending.Event().Target; got != "ci" {
		t.Fatalf("target = %q, want the key's name", got)
	}
}
