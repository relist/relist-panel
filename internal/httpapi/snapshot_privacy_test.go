package httpapi_test

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestSnapshotSecretsRemainPrivateAcrossDeploymentAPIs(t *testing.T) {
	server := newDeploymentsServer(t)
	f := newDeploymentsFixture(t, server, "privacy", "web", plainYAML)
	f.addZipSource(t)
	secrets := []string{"snapshot-environment-secret", "snapshot-compose-secret", "snapshot-private-key"}
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO environment_variables (environment_id, key, value, secret) VALUES (?, 'ENV_TOKEN', ?, 1)`, []any{f.env.ID, secrets[0]}},
		{`INSERT INTO compose_variables (compose_id, key, value, secret) VALUES (?, 'APP_TOKEN', ?, 1)`, []any{f.compose.ID, secrets[1]}},
		{`INSERT INTO ssh_keys (id, name, private_key, public_key, created_at) VALUES (1, 'privacy', ?, 'ssh-ed25519 AAAA', 1)`, []any{secrets[2]}},
		{`UPDATE compose_sources SET ssh_key_id = 1 WHERE compose_id = ?`, []any{f.compose.ID}},
	} {
		if _, err := server.db.Exec(query.sql, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	deployment := server.deployAndWait(t, f.path(), map[string]any{"mode": "build"})
	if deployment.Status != "succeeded" {
		t.Fatalf("deployment status = %q, reason = %q", deployment.Status, deployment.Reason)
	}
	var private string
	if err := server.db.QueryRow(`SELECT snapshot FROM deployment_snapshots JOIN deployments ON deployments.id = deployment_snapshots.deployment_id WHERE compose_id = ? AND number = ?`, f.compose.ID, deployment.Number).Scan(&private); err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if !strings.Contains(private, secret) {
			t.Fatal("test fixture did not persist the expected private input")
		}
	}
	detail := f.path() + "/deployments/" + strconv.Itoa(deployment.Number)
	for _, path := range []string{f.path() + "/deployments", detail, detail + "/log?offset=0", "/api/deployments", "/api/deployments/queue", f.path() + "/source"} {
		response := server.request(t, http.MethodGet, path, nil)
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status = %d, error = %v", path, response.StatusCode, err)
		}
		for _, secret := range secrets {
			if strings.Contains(string(body), secret) {
				t.Fatalf("GET %s disclosed private snapshot input", path)
			}
		}
	}
}
