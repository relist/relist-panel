package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestNoSecretValueReachesAViewerOrOperator(t *testing.T) {
	f := newSetsFixture(t, setsYAML)
	const (
		envSecret     = "env-secret-0f3a"
		composeSecret = "compose-secret-9b1c"
		setSecret     = "set-secret-77de"
		plain         = "plain-value-5e2d"
	)
	for _, v := range []struct {
		key, value string
		secret     bool
	}{{"ENV_SECRET", envSecret, true}, {"ENV_PLAIN", plain, false}} {
		response := postJSON(t, f.server, environmentVariablesPath(f.projectID, f.environmentID), map[string]any{"key": v.key, "value": v.value, "secret": v.secret})
		response.Body.Close()
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("create %s = %d", v.key, response.StatusCode)
		}
	}
	f.create(t, "COMPOSE_SECRET", composeSecret, true)
	set := f.createSet(t, "shared")
	f.addEntry(t, set, "SET_SECRET", setSecret, true)
	if status, raw := f.call(t, http.MethodPut, f.bindingsPath("/api"), map[string]any{"setIds": []int64{set.ID}}); status != http.StatusOK {
		t.Fatalf("bind = %d %s", status, raw)
	}

	base := composePath(f.projectID, f.environmentID, f.composeID)
	reads := []string{
		environmentVariablesPath(f.projectID, f.environmentID),
		base + "/variables",
		base + "/variable-sets",
		base + "/services/api/effective-variables",
		base,
	}
	secrets := []string{envSecret, composeSecret, setSecret}
	for i, role := range []string{"viewer", "operator", "editor"} {
		userID := int64(10 + i)
		signInAsUser(t, f.server, userID, role+"@example.com", false)
		if _, err := f.server.db.Exec("INSERT INTO grants (user_id, project_id, environment_id, role) VALUES (?, ?, NULL, ?)", userID, f.projectID, role); err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, path := range reads {
			status, raw := f.call(t, http.MethodGet, path, nil)
			if status != http.StatusOK {
				t.Fatalf("%s GET %s = %d %s", role, path, status, raw)
			}
			for _, value := range append(secrets, plain) {
				if strings.Contains(string(raw), value) {
					seen[value] = true
				}
			}
		}
		if !seen[plain] {
			t.Errorf("%s never saw the plain value", role)
		}
		for _, secret := range secrets {
			if want := role == "editor"; seen[secret] != want {
				t.Errorf("%s saw secret %q = %v, want %v", role, secret, seen[secret], want)
			}
		}
		// Preview resolves an unsaved file with every value: only an admin,
		// who alone writes the file, reaches it (ADR-0032).
		if status, _ := f.call(t, http.MethodPost, base+"/preview", map[string]any{}); status != http.StatusForbidden {
			t.Errorf("%s preview = %d, want 403", role, status)
		}
	}
}
