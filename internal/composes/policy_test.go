package composes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// policyConfig is a resolved config binding src/conf through path, with
// its env file at envFile.
func policyConfig(path, envFile string) []byte {
	return []byte(fmt.Sprintf(`{"name":"relist-c1","services":{"web":{"image":"nginx","env_file":[{"path":%q}],"volumes":[{"type":"bind","source":%q,"target":"/c"}]}}}`, envFile, path))
}

// The same saved config hashes the same however it was staged and whichever
// Deployment's private Source copy it points at.
func TestPolicyHashIsStableAcrossStagingAndSourceCopies(t *testing.T) {
	svc, runner, target, _ := newDeployService(t, "services: {web: {image: nginx}}\n")
	dir := ComposeDir(target.Request)
	hashes := []string{}
	for _, copy := range []string{"source-a", "source-b"} {
		runner.onConfig = func(req RunRequest) {
			runner.config = policyConfig(filepath.Join(dir, ".deployment-sources", copy, "src", "conf"), req.EnvFilePath)
		}
		result, err := svc.EvaluatePolicy(context.Background(), target)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Violations) != 0 {
			t.Fatalf("violations = %+v", result.Violations)
		}
		hashes = append(hashes, result.Hash)
	}
	if hashes[0] != hashes[1] || hashes[0] == "" {
		t.Fatalf("hashes = %v", hashes)
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".policy-") {
			t.Errorf("staging left behind: %s", entry.Name())
		}
	}
}

func TestApprovalMatchesOnlyItsHash(t *testing.T) {
	svc, runner, target, _ := newDeployService(t, "services: {web: {image: nginx}}\n")
	runner.config = []byte(`{"name":"relist-c1","services":{"web":{"image":"nginx","privileged":true}}}`)
	result, err := svc.EvaluatePolicy(context.Background(), target)
	if err != nil || len(result.Violations) != 1 || result.Approved {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if err := svc.Approve(context.Background(), target.Compose.ID, result.Hash, 0); err != nil {
		t.Fatal(err)
	}
	if again, _ := svc.EvaluatePolicy(context.Background(), target); !again.Approved {
		t.Fatal("approval did not match its hash")
	}
	runner.config = []byte(`{"name":"relist-c1","services":{"web":{"image":"nginx:2","privileged":true}}}`)
	if changed, _ := svc.EvaluatePolicy(context.Background(), target); changed.Approved {
		t.Fatal("approval matched a changed config")
	}
}

func TestPolicyEvaluationErrorIsAnError(t *testing.T) {
	svc, runner, target, _ := newDeployService(t, "services: {web: {image: nginx}}\n")
	runner.configErr = fmt.Errorf("invalid compose project")
	if _, err := svc.EvaluatePolicy(context.Background(), target); err == nil {
		t.Fatal("a config failure was not an error")
	}
}
