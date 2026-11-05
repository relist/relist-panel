package certificates

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relist-panel/internal/audit"
)

func TestCreateNamesTheCertificateInTheAuditEvent(t *testing.T) {
	ts := newService(t)
	raw, _ := json.Marshal(pairBody(t, "wildcard", "*.example.com"))
	ctx, pending := audit.Begin(context.Background(), audit.Event{}, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/certificates", strings.NewReader(string(raw))).WithContext(ctx)
	rec := httptest.NewRecorder()
	ts.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if got := pending.Event().Target; got != "wildcard" {
		t.Fatalf("target = %q, want the certificate's name", got)
	}
}
