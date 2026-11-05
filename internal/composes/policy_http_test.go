package composes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relist-panel/internal/auth"
	"relist-panel/internal/policy"
)

// With RELIST_POLICY_APPROVER=owner only users.id 1 approves; other admins
// are refused.
func TestOwnerApproverRefusesOtherAdmins(t *testing.T) {
	svc, runner, target, db := newDeployService(t, "services: {web: {image: nginx}}\n")
	if _, err := db.Exec("INSERT INTO users (id, email, password_hash, is_admin, created_at) VALUES (1, 'owner@example.com', 'x', 1, 1), (2, 'admin@example.com', 'x', 1, 1)"); err != nil {
		t.Fatal(err)
	}
	runner.config = []byte(`{"name":"x","services":{"web":{"image":"nginx","privileged":true}}}`)
	svc.SetApprover(policy.ApproverOwner)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	result, err := svc.EvaluatePolicy(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/projects/7/environments/3/composes/1/approval"
	approve := func(user auth.User) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"configHash":"`+result.Hash+`"}`))
		req = req.WithContext(auth.WithUser(req.Context(), user))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := approve(auth.User{ID: 2, Email: "admin@example.com", IsAdmin: true}); got != http.StatusForbidden {
		t.Fatalf("admin under owner = %d", got)
	}
	if got := approve(auth.User{ID: 1, Email: "owner@example.com", IsAdmin: true}); got != http.StatusNoContent {
		t.Fatalf("owner = %d", got)
	}
}
