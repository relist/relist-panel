package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"relist-panel/internal/terminal"
)

type auditRow struct {
	Action  string
	Outcome string
	Actor   string
	Target  string
	Detail  map[string]any
}

// auditRows reads every event, oldest first, straight from the table.
func auditRows(t *testing.T, server *testServer) []auditRow {
	t.Helper()
	rows, err := server.db.Query("SELECT action, outcome, actor, target, COALESCE(detail, '') FROM audit_events ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var row auditRow
		var detail string
		if err := rows.Scan(&row.Action, &row.Outcome, &row.Actor, &row.Target, &detail); err != nil {
			t.Fatal(err)
		}
		if detail != "" {
			if err := json.Unmarshal([]byte(detail), &row.Detail); err != nil {
				t.Fatal(err)
			}
		}
		out = append(out, row)
	}
	return out
}

func auditActions(rows []auditRow, action string) []auditRow {
	var out []auditRow
	for _, row := range rows {
		if row.Action == action {
			out = append(out, row)
		}
	}
	return out
}

func TestNoVariableValueInAudit(t *testing.T) {
	f := newSetsFixture(t, setsYAML)
	const value = "s3cr3t-v4lue"
	envPath := environmentVariablesPath(f.projectID, f.environmentID)
	for _, secret := range []bool{true, false} {
		key := fmt.Sprintf("ENV_%v", secret)
		status, raw := f.call(t, http.MethodPost, envPath, map[string]any{"key": key, "value": value, "secret": secret})
		if status != http.StatusCreated {
			t.Fatalf("create env %s = %d %s", key, status, raw)
		}
		var created struct{ ID int64 }
		_ = json.Unmarshal(raw, &created)
		if status, raw := f.call(t, http.MethodPut, fmt.Sprintf("%s/%d", envPath, created.ID), map[string]any{"key": key, "value": value + "2", "secret": secret}); status != http.StatusOK {
			t.Fatalf("update env %s = %d %s", key, status, raw)
		}
		variable := f.create(t, "COMPOSE_"+key, value, secret)
		if status, raw := f.call(t, http.MethodPut, f.path(fmt.Sprintf("/%d", variable.ID)), map[string]any{"key": "COMPOSE_" + key, "value": value + "2", "secret": secret}); status != http.StatusOK {
			t.Fatalf("update compose variable = %d %s", status, raw)
		}
	}
	set := f.createSet(t, "shared")
	entry := f.addEntry(t, set, "SET_KEY", value, true)
	if status, raw := f.call(t, http.MethodPut, f.setsPath(fmt.Sprintf("/%d/entries/%d", set.ID, entry.ID)), map[string]any{"key": "SET_KEY", "value": value + "2", "secret": true}); status != http.StatusOK {
		t.Fatalf("update entry = %d %s", status, raw)
	}
	// An Editor's reads of the lists are recorded too, without values.
	signInAsUser(t, f.server, 10, "editor@example.com", false)
	if _, err := f.server.db.Exec("INSERT INTO grants (user_id, project_id, environment_id, role) VALUES (10, ?, NULL, 'editor')", f.projectID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{envPath, f.path(""), f.setsPath(""), composePath(f.projectID, f.environmentID, f.composeID) + "/services/api/effective-variables"} {
		if status, raw := f.call(t, http.MethodGet, path, nil); status != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, status, raw)
		}
	}

	raw, err := f.server.db.Query("SELECT target || ' ' || COALESCE(detail, '') FROM audit_events")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	count := 0
	for raw.Next() {
		var text string
		_ = raw.Scan(&text)
		count++
		if strings.Contains(text, value) {
			t.Errorf("an audit event holds a variable value: %s", text)
		}
	}
	rows := auditRows(t, f.server)
	if count == 0 {
		t.Fatal("nothing recorded")
	}
	for action, key := range map[string]string{
		"environment_variable.create": "ENV_true",
		"environment_variable.update": "ENV_true",
		"compose_variable.create":     "COMPOSE_ENV_true",
		"compose_variable.update":     "COMPOSE_ENV_true",
		"variable_set_entry.create":   "SET_KEY",
		"variable_set_entry.update":   "SET_KEY",
	} {
		events := auditActions(rows, action)
		if len(events) == 0 || events[0].Detail["key"] != key {
			t.Errorf("%s events = %+v, want detail.key %s first", action, events, key)
		}
	}
	if sets := auditActions(rows, "variable_set.create"); len(sets) != 1 || sets[0].Detail["name"] != "shared" {
		t.Errorf("variable_set.create = %+v", sets)
	}
}

func TestSecretReadIsRecorded(t *testing.T) {
	f := newSetsFixture(t, setsYAML)
	envPath := environmentVariablesPath(f.projectID, f.environmentID)
	if status, raw := f.call(t, http.MethodPost, envPath, map[string]any{"key": "TOKEN", "value": "t0k3n", "secret": true}); status != http.StatusCreated {
		t.Fatalf("create = %d %s", status, raw)
	}
	f.create(t, "PLAIN", "p", false)
	for i, role := range []string{"viewer", "editor"} {
		id := int64(10 + i)
		signInAsUser(t, f.server, id, role+"@example.com", false)
		if _, err := f.server.db.Exec("INSERT INTO grants (user_id, project_id, environment_id, role) VALUES (?, ?, NULL, ?)", id, f.projectID, role); err != nil {
			t.Fatal(err)
		}
		f.call(t, http.MethodGet, envPath, nil)
		f.call(t, http.MethodGet, f.path(""), nil) // no secret here
	}
	reads := auditActions(auditRows(t, f.server), "secret.read")
	if len(reads) != 1 || reads[0].Actor != "editor@example.com" || fmt.Sprint(reads[0].Detail["keys"]) != "[TOKEN]" ||
		reads[0].Target != "Shop / production" {
		t.Fatalf("secret.read = %+v", reads)
	}
}

func TestGrantEventNamesTheUser(t *testing.T) {
	f := newRoleFixture(t)
	signInAsUser(t, f.server, 3, "admin2@example.com", true)
	response := f.server.request(t, http.MethodPut, "/api/users/2/grants", []map[string]any{{"projectId": f.alpha.ID, "environmentId": nil, "role": "viewer"}})
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("PUT grants = %d", response.StatusCode)
	}
	events := auditActions(auditRows(t, f.server), "grant.set")
	if len(events) != 1 || events[0].Target != "member@example.com" || events[0].Actor != "admin2@example.com" {
		t.Fatalf("grant.set = %+v", events)
	}
	grants, _ := events[0].Detail["grants"].([]any)
	if len(grants) != 1 {
		t.Fatalf("detail = %+v", events[0].Detail)
	}
}

func TestTerminalOpenAndCloseAreRecorded(t *testing.T) {
	server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
	scriptAppContainer(server, projectName, terminal.Container{ID: "c1", Name: "relist-c3-app-1", Number: 1}, "/bin/bash")
	conn, _ := dialReady(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"))
	if events := auditActions(auditRows(t, server), "terminal.open"); len(events) != 1 || events[0].Detail["service"] != "app" {
		t.Fatalf("terminal.open = %+v", events)
	}
	conn.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(3 * time.Second)
	for {
		closes := auditActions(auditRows(t, server), "terminal.close")
		if len(closes) == 1 {
			if seconds, ok := closes[0].Detail["seconds"].(float64); !ok || seconds < 0 {
				t.Fatalf("terminal.close = %+v", closes[0])
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no terminal.close after the socket closed: %+v", closes)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestProjectAuditShowsOnlyEditedEnvironments(t *testing.T) {
	f := newRoleFixture(t)
	insert := func(action string, projectID, environmentID any) {
		if _, err := f.server.db.Exec("INSERT INTO audit_events (at, actor, action, outcome, project_id, environment_id, target) VALUES (1, 'x', ?, 'ok', ?, ?, 't')",
			action, projectID, environmentID); err != nil {
			t.Fatal(err)
		}
	}
	// Start from the events below only, not the fixture's own setup.
	if _, err := f.server.db.Exec("DELETE FROM audit_events"); err != nil {
		t.Fatal(err)
	}
	insert("prod.change", f.alpha.ID, f.alphaProd.ID)
	insert("staging.change", f.alpha.ID, f.alphaStaging.ID)
	insert("project.update", f.alpha.ID, nil)
	insert("beta.change", f.beta.ID, f.betaE.ID)
	path := fmt.Sprintf("/api/projects/%d/audit", f.alpha.ID)
	actionsOf := func(body string) []string {
		var page struct {
			Items []struct{ Action string } `json:"items"`
		}
		_ = json.Unmarshal([]byte(body), &page)
		var out []string
		for _, item := range page.Items {
			out = append(out, item.Action)
		}
		return out
	}

	f.grant(t, f.alpha.ID, f.alphaProd.ID, "viewer")
	if code, body := f.get(t, path); code != http.StatusForbidden {
		t.Fatalf("viewer only = %d %s", code, body)
	}
	f.grant(t, f.alpha.ID, f.alphaStaging.ID, "editor")
	code, body := f.get(t, path)
	if got := strings.Join(actionsOf(body), ","); code != http.StatusOK || got != "project.update,staging.change" {
		t.Fatalf("editor of staging = %d %s", code, got)
	}
	if code, _ := f.get(t, fmt.Sprintf("/api/projects/%d/audit", f.beta.ID)); code != http.StatusNotFound {
		t.Fatalf("hidden project = %d", code)
	}
	if code, _ := f.get(t, "/api/audit"); code != http.StatusForbidden {
		t.Fatalf("member GET /api/audit = %d", code)
	}
	signInAsUser(t, f.server, 3, "admin2@example.com", true)
	if _, body := f.get(t, path); strings.Join(actionsOf(body), ",") != "project.update,staging.change,prod.change" {
		t.Fatalf("admin = %s", body)
	}
}

// A create event names what it created, not only the parent it was made in.
func TestCreateEventsNameWhatWasCreated(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "alpha", "")
	response := postJSON(t, server, fmt.Sprintf("/api/projects/%d/environments", project.ID), map[string]string{"name": "staging"})
	var environment struct{ ID int64 }
	_ = json.NewDecoder(response.Body).Decode(&environment)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create environment = %d", response.StatusCode)
	}
	createCompose(t, server, project.ID, environment.ID, "api")
	rows := auditRows(t, server)
	for action, want := range map[string]string{"environment.create": "staging", "compose.create": "api"} {
		events := auditActions(rows, action)
		if len(events) != 1 || events[0].Detail["name"] != want {
			t.Errorf("%s: events = %+v, want detail name %q", action, events, want)
		}
	}
}
