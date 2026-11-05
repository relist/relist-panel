package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const privilegedConfig = `{"name":"x","services":{"web":{"image":"nginx","privileged":true,"environment":{"TOKEN":"s3cr3t"}}}}`

type policyBody struct {
	Violations []struct{ Service, Message string } `json:"violations"`
	ConfigHash *string                             `json:"configHash"`
	Approved   bool                                `json:"approved"`
}

func policyPath(f *roleFixture) string {
	return fmt.Sprintf("/api/projects/%d/environments/%d/composes/%d/policy", f.alpha.ID, f.alphaProd.ID, f.alphaWeb.ID)
}

func approvalPath(f *roleFixture) string {
	return fmt.Sprintf("/api/projects/%d/environments/%d/composes/%d/approval", f.alpha.ID, f.alphaProd.ID, f.alphaWeb.ID)
}

// A Viewer sees what is wrong, never the hash over interpolated secrets.
func TestPolicyShowsViolationsAndTheHashOnlyToApprovers(t *testing.T) {
	f := newRoleFixture(t)
	f.server.runner.setPolicyConfig(privilegedConfig)
	f.grant(t, f.alpha.ID, f.alphaProd.ID, "viewer")
	code, raw := f.get(t, policyPath(f))
	var body policyBody
	_ = json.Unmarshal([]byte(raw), &body)
	if code != http.StatusOK || len(body.Violations) != 1 || body.ConfigHash != nil || body.Approved {
		t.Fatalf("viewer = %d %s", code, raw)
	}
	if strings.Contains(raw, "s3cr3t") {
		t.Fatalf("a value reached the viewer: %s", raw)
	}
	signInAsUser(t, f.server, 3, "admin2@example.com", true)
	_, raw = f.get(t, policyPath(f))
	_ = json.Unmarshal([]byte(raw), &body)
	if body.ConfigHash == nil || len(*body.ConfigHash) != 64 {
		t.Fatalf("approver = %s", raw)
	}
}

func TestApprovalNeedsTheHashTheApproverSaw(t *testing.T) {
	f := newRoleFixture(t)
	f.server.runner.setPolicyConfig(privilegedConfig)
	signInAsUser(t, f.server, 3, "admin2@example.com", true)
	response := f.server.request(t, http.MethodPost, approvalPath(f), map[string]string{"configHash": strings.Repeat("0", 64)})
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale approval = %d", response.StatusCode)
	}
	_, raw := f.get(t, policyPath(f))
	var body policyBody
	_ = json.Unmarshal([]byte(raw), &body)
	response = f.server.request(t, http.MethodPost, approvalPath(f), map[string]string{"configHash": *body.ConfigHash})
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("approval = %d", response.StatusCode)
	}
	_, raw = f.get(t, policyPath(f))
	if err := json.Unmarshal([]byte(raw), &body); err != nil || !body.Approved {
		t.Fatalf("after approval = %s", raw)
	}
	if events := auditActions(auditRows(t, f.server), "compose.approve"); len(events) != 1 || events[0].Actor != "admin2@example.com" {
		t.Fatalf("compose.approve = %+v", events)
	}
}

// An Editor cannot approve: the route is an approver's.
func TestEditorCannotApprove(t *testing.T) {
	f := newRoleFixture(t)
	f.server.runner.setPolicyConfig(privilegedConfig)
	f.grant(t, f.alpha.ID, f.alphaProd.ID, "editor")
	response := f.server.request(t, http.MethodPost, approvalPath(f), map[string]string{"configHash": strings.Repeat("0", 64)})
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("editor approval = %d", response.StatusCode)
	}
}

func TestStatusSaysWhetherTheUserIsAnApprover(t *testing.T) {
	f := newRoleFixture(t)
	if _, body := f.get(t, "/api/auth/status"); !strings.Contains(body, `"isApprover":false`) {
		t.Fatalf("member status = %s", body)
	}
	signInAsUser(t, f.server, 3, "admin2@example.com", true)
	if _, body := f.get(t, "/api/auth/status"); !strings.Contains(body, `"isApprover":true`) {
		t.Fatalf("admin status = %s", body)
	}
}
