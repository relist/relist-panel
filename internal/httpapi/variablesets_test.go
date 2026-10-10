package httpapi_test

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type setEntryResponse struct {
	ID     int64  `json:"id"`
	SetID  int64  `json:"setId"`
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

type binderResponse struct {
	ComposeID   int64  `json:"composeId"`
	ComposeName string `json:"composeName"`
	Service     string `json:"service"`
}

type setResponse struct {
	ID            int64              `json:"id"`
	EnvironmentID int64              `json:"environmentId"`
	Name          string             `json:"name"`
	Entries       []setEntryResponse `json:"entries"`
	UsedBy        []binderResponse   `json:"usedBy"`
	Error         string             `json:"error"`
}

const setsYAML = "services:\n  api: {image: nginx}\n  redis: {image: redis}\n"

type setsFixture struct {
	composeVariablesFixture
}

func newSetsFixture(t *testing.T, yaml string) setsFixture {
	t.Helper()
	server := newTestServer(t)
	setupAndLogin(t, server)
	return setsFixture{newComposeVariablesFixture(t, server, yaml)}
}

func (f setsFixture) setsPath(suffix string) string {
	return environmentPath(f.projectID, f.environmentID) + "/variable-sets" + suffix
}

func (f setsFixture) bindingsPath(suffix string) string {
	return composePath(f.projectID, f.environmentID, f.composeID) + "/bindings" + suffix
}

func (f setsFixture) call(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	response := f.server.request(t, method, path, body)
	defer response.Body.Close()
	var raw bytes.Buffer
	_, _ = raw.ReadFrom(response.Body)
	return response.StatusCode, raw.Bytes()
}

func (f setsFixture) createSet(t *testing.T, name string) setResponse {
	t.Helper()
	status, raw := f.call(t, http.MethodPost, f.setsPath(""), map[string]any{"name": name})
	if status != http.StatusCreated {
		t.Fatalf("create set %s status = %d, body %s", name, status, raw)
	}
	var set setResponse
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	return set
}

func (f setsFixture) addEntry(t *testing.T, set setResponse, key, value string, secret bool) setEntryResponse {
	t.Helper()
	status, raw := f.call(t, http.MethodPost, f.setsPath("/"+idString(set.ID)+"/entries"), map[string]any{"key": key, "value": value, "secret": secret})
	if status != http.StatusCreated {
		t.Fatalf("add entry %s status = %d, body %s", key, status, raw)
	}
	var entry setEntryResponse
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatal(err)
	}
	return entry
}

func (f setsFixture) bind(t *testing.T, service string, sets ...setResponse) {
	t.Helper()
	ids := []int64{}
	for _, set := range sets {
		ids = append(ids, set.ID)
	}
	status, raw := f.call(t, http.MethodPut, f.bindingsPath("/"+service), map[string]any{"setIds": ids})
	if status != http.StatusOK {
		t.Fatalf("bind %s status = %d, body %s", service, status, raw)
	}
}

func (f setsFixture) listSets(t *testing.T) []setResponse {
	t.Helper()
	status, raw := f.call(t, http.MethodGet, f.setsPath(""), nil)
	var sets []setResponse
	if status != http.StatusOK || json.Unmarshal(raw, &sets) != nil {
		t.Fatalf("list sets = %d %s", status, raw)
	}
	return sets
}

// deployed is what one deploy wrote: the resolved YAML's Services' env_file
// lists and the runner's request.
type deployed struct {
	envFiles    map[string][]string
	composeFile string
	envFile     string
	warnings    []string
	status      int
}

func (f setsFixture) deploy(t *testing.T) deployed {
	t.Helper()
	f.server.runner.calls = nil
	status, raw := f.call(t, http.MethodPost, composeDeployPath(f.projectID, f.environmentID, f.composeID), nil)
	if status != http.StatusOK {
		t.Fatalf("deploy status = %d, body %s", status, raw)
	}
	var body struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	for _, call := range f.server.runner.calls {
		if call.method != "up" {
			continue
		}
		content, err := os.ReadFile(call.request.ComposeFilePath)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Services map[string]struct {
				EnvFile []string `yaml:"env_file"`
			} `yaml:"services"`
		}
		if err := yaml.Unmarshal(content, &doc); err != nil {
			t.Fatal(err)
		}
		files := map[string][]string{}
		for name, service := range doc.Services {
			files[name] = service.EnvFile
		}
		return deployed{envFiles: files, composeFile: call.request.ComposeFilePath, envFile: call.request.EnvFilePath, warnings: body.Warnings, status: status}
	}
	t.Fatal("deploy never reached runner")
	return deployed{}
}

func (d deployed) setPath(set setResponse) string {
	return filepath.Join(filepath.Dir(d.envFile), "sets", idString(set.ID)+".env")
}

func TestVariableSetsCRUD(t *testing.T) {
	f := newSetsFixture(t, setsYAML)

	if status, raw := f.call(t, http.MethodGet, f.setsPath(""), nil); status != http.StatusOK || strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("empty list = %d %s", status, raw)
	}
	set := f.createSet(t, "db-creds")
	if set.ID == 0 || set.Name != "db-creds" || set.EnvironmentID != f.environmentID || len(set.Entries) != 0 || set.UsedBy == nil {
		t.Fatalf("created = %+v", set)
	}

	entry := f.addEntry(t, set, "DB_URL", "postgres://x", false)
	secret := f.addEntry(t, set, "DB_PASSWORD", "hunter2", true)
	if entry.SetID != set.ID || !secret.Secret {
		t.Fatalf("entries = %+v %+v", entry, secret)
	}

	status, raw := f.call(t, http.MethodPut, f.setsPath("/"+idString(set.ID)+"/entries/"+idString(entry.ID)), map[string]any{"key": "DB_URL", "value": "postgres://y", "secret": true})
	var updated setEntryResponse
	if status != http.StatusOK || json.Unmarshal(raw, &updated) != nil || updated.Value != "postgres://y" || !updated.Secret || updated.ID != entry.ID {
		t.Fatalf("update entry = %d %s", status, raw)
	}

	status, raw = f.call(t, http.MethodPut, f.setsPath("/"+idString(set.ID)), map[string]any{"name": "database"})
	var renamed setResponse
	if status != http.StatusOK || json.Unmarshal(raw, &renamed) != nil || renamed.Name != "database" || len(renamed.Entries) != 2 {
		t.Fatalf("rename = %d %s", status, raw)
	}

	sets := f.listSets(t)
	if len(sets) != 1 || sets[0].Name != "database" || len(sets[0].Entries) != 2 || sets[0].Entries[0].Value != "postgres://y" || sets[0].Entries[1].Key != "DB_PASSWORD" {
		t.Fatalf("list = %+v", sets)
	}

	if status, _ = f.call(t, http.MethodDelete, f.setsPath("/"+idString(set.ID)+"/entries/"+idString(entry.ID)), nil); status != http.StatusNoContent {
		t.Fatalf("delete entry status = %d", status)
	}
	if status, _ = f.call(t, http.MethodDelete, f.setsPath("/"+idString(set.ID)+"/entries/"+idString(entry.ID)), nil); status != http.StatusNotFound {
		t.Fatalf("second delete entry status = %d, want 404", status)
	}
	if status, _ = f.call(t, http.MethodDelete, f.setsPath("/"+idString(set.ID)), nil); status != http.StatusNoContent {
		t.Fatalf("delete set status = %d", status)
	}
	if got := f.listSets(t); len(got) != 0 {
		t.Fatalf("sets after delete = %+v", got)
	}
	var left int
	if err := f.server.db.QueryRow("SELECT COUNT(*) FROM variable_set_entries").Scan(&left); err != nil || left != 0 {
		t.Fatalf("entries left after set delete = %d (%v)", left, err)
	}
}

func TestVariableSetValidation(t *testing.T) {
	f := newSetsFixture(t, setsYAML)
	set := f.createSet(t, "stripe")
	other := f.createSet(t, "other")
	f.addEntry(t, set, "KEY", "v", false)

	long := strings.Repeat("a", 64)
	for name, body := range map[string]map[string]any{
		"empty":        {"name": ""},
		"uppercase":    {"name": "Stripe"},
		"underscore":   {"name": "my_set"},
		"digit first":  {"name": "1set"},
		"dash first":   {"name": "-set"},
		"too long":     {"name": long},
		"missing name": {},
		"unknown":      {"name": "ok", "extra": 1},
	} {
		if status, raw := f.call(t, http.MethodPost, f.setsPath(""), body); status != http.StatusBadRequest {
			t.Errorf("create %s: status = %d, want 400 (%s)", name, status, raw)
		}
	}
	if status, raw := f.call(t, http.MethodPost, f.setsPath(""), map[string]any{"name": strings.Repeat("a", 63)}); status != http.StatusCreated {
		t.Errorf("63-char name status = %d, want 201 (%s)", status, raw)
	}
	if status, _ := f.call(t, http.MethodPost, f.setsPath(""), map[string]any{"name": "stripe"}); status != http.StatusConflict {
		t.Errorf("duplicate name status = %d, want 409", status)
	}
	if status, _ := f.call(t, http.MethodPut, f.setsPath("/"+idString(other.ID)), map[string]any{"name": "stripe"}); status != http.StatusConflict {
		t.Errorf("rename onto existing status = %d, want 409", status)
	}
	if status, _ := f.call(t, http.MethodPut, f.setsPath("/"+idString(other.ID)), map[string]any{"name": "Bad"}); status != http.StatusBadRequest {
		t.Errorf("invalid rename status = %d, want 400", status)
	}

	entries := f.setsPath("/" + idString(set.ID) + "/entries")
	for name, body := range map[string]map[string]any{
		"dash in key":   {"key": "MY-KEY", "value": "v"},
		"empty key":     {"key": " ", "value": "v"},
		"missing value": {"key": "NOVALUE"},
	} {
		if status, raw := f.call(t, http.MethodPost, entries, body); status != http.StatusBadRequest {
			t.Errorf("entry %s: status = %d, want 400 (%s)", name, status, raw)
		}
	}
	if status, _ := f.call(t, http.MethodPost, entries, map[string]any{"key": "KEY", "value": "x"}); status != http.StatusConflict {
		t.Errorf("duplicate key status = %d, want 409", status)
	}
	// The same key and name in another set or Environment is fine.
	if status, _ := f.call(t, http.MethodPost, f.setsPath("/"+idString(other.ID)+"/entries"), map[string]any{"key": "KEY", "value": "x"}); status != http.StatusCreated {
		t.Errorf("same key in another set status = %d, want 201", status)
	}
}

func TestVariableSetRouteScoping(t *testing.T) {
	f := newSetsFixture(t, setsYAML)
	set := f.createSet(t, "stripe")
	entry := f.addEntry(t, set, "A", "1", false)

	otherProject := createProject(t, f.server, "Blog", "")
	otherEnvironment := defaultEnvironment(t, f.server, otherProject.ID)
	crossBase := environmentPath(otherProject.ID, otherEnvironment.ID) + "/variable-sets/" + idString(set.ID)
	for _, c := range []struct{ method, path string }{
		{http.MethodPut, crossBase},
		{http.MethodDelete, crossBase},
		{http.MethodPost, crossBase + "/entries"},
		{http.MethodPut, crossBase + "/entries/" + idString(entry.ID)},
		{http.MethodDelete, crossBase + "/entries/" + idString(entry.ID)},
		{http.MethodGet, environmentPath(f.projectID, 9999) + "/variable-sets"},
		{http.MethodGet, f.bindingsPath("")[:len(f.bindingsPath(""))-len("/bindings")] + "9/bindings"},
	} {
		if status, raw := f.call(t, c.method, c.path, map[string]any{"name": "x", "key": "B", "value": "2"}); status != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 404 (%s)", c.method, c.path, status, raw)
		}
	}
	if sets := f.listSets(t); len(sets) != 1 || len(sets[0].Entries) != 1 || sets[0].Name != "stripe" {
		t.Errorf("set changed through a wrong route: %+v", sets)
	}
}

func TestDeletingABoundSetIsRefusedUntilUnbound(t *testing.T) {
	f := newSetsFixture(t, setsYAML)
	stripe := f.createSet(t, "stripe")
	f.addEntry(t, stripe, "K", "v", false)
	f.bind(t, "api", stripe)
	f.bind(t, "worker", stripe)

	status, raw := f.call(t, http.MethodDelete, f.setsPath("/"+idString(stripe.ID)), nil)
	var body struct {
		Error  string           `json:"error"`
		UsedBy []binderResponse `json:"usedBy"`
	}
	if status != http.StatusConflict || json.Unmarshal(raw, &body) != nil || body.Error == "" {
		t.Fatalf("delete bound status = %d, body %s", status, raw)
	}
	want := []binderResponse{{f.composeID, "web", "api"}, {f.composeID, "web", "worker"}}
	if !reflect.DeepEqual(body.UsedBy, want) {
		t.Fatalf("usedBy = %+v, want %+v", body.UsedBy, want)
	}
	if sets := f.listSets(t); len(sets) != 1 || !reflect.DeepEqual(sets[0].UsedBy, want) || len(sets[0].Entries) != 1 {
		t.Fatalf("set after refused delete = %+v", sets)
	}

	f.bind(t, "api")
	f.bind(t, "worker")
	if status, _ = f.call(t, http.MethodDelete, f.setsPath("/"+idString(stripe.ID)), nil); status != http.StatusNoContent {
		t.Fatalf("delete after unbind status = %d", status)
	}
}

func TestDeletingAnEnvironmentWithBoundSetsSucceeds(t *testing.T) {
	f := newSetsFixture(t, setsYAML)
	status, raw := f.call(t, http.MethodPost, projectEnvironmentsPath(f.projectID), map[string]any{"name": "Staging"})
	var staging environmentResponse
	if status != http.StatusCreated || json.Unmarshal(raw, &staging) != nil {
		t.Fatalf("create environment = %d %s", status, raw)
	}
	g := setsFixture{composeVariablesFixture{server: f.server, projectID: f.projectID, environmentID: staging.ID}}
	created := postJSON(t, f.server, composesPath(f.projectID, staging.ID), map[string]string{"name": "app", "yamlContent": setsYAML})
	var compose composeResponse
	_ = json.NewDecoder(created.Body).Decode(&compose)
	created.Body.Close()
	g.composeID = compose.ID

	set := g.createSet(t, "stripe")
	g.addEntry(t, set, "K", "v", false)
	g.bind(t, "api", set)

	if status, raw = f.call(t, http.MethodDelete, environmentPath(f.projectID, staging.ID), nil); status != http.StatusNoContent {
		t.Fatalf("delete environment status = %d, body %s", status, raw)
	}
	for _, table := range []string{"variable_sets", "variable_set_entries", "service_bindings"} {
		var count int
		if err := f.server.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Errorf("%s rows after environment delete = %d (%v)", table, count, err)
		}
	}
}

func TestBindingsAPI(t *testing.T) {
	f := newSetsFixture(t, setsYAML)
	a, b, c := f.createSet(t, "a"), f.createSet(t, "b"), f.createSet(t, "c")

	if status, raw := f.call(t, http.MethodGet, f.bindingsPath(""), nil); status != http.StatusOK || strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("empty bindings = %d %s", status, raw)
	}
	f.bind(t, "redis", c)
	f.bind(t, "api", c, a, b)
	// A Service the YAML does not declare is fine.
	f.bind(t, "gone", b)

	status, raw := f.call(t, http.MethodGet, f.bindingsPath(""), nil)
	var groups []struct {
		Service string  `json:"service"`
		SetIDs  []int64 `json:"setIds"`
	}
	if status != http.StatusOK || json.Unmarshal(raw, &groups) != nil {
		t.Fatalf("bindings = %d %s", status, raw)
	}
	if len(groups) != 3 || groups[0].Service != "api" || !reflect.DeepEqual(groups[0].SetIDs, []int64{c.ID, a.ID, b.ID}) || groups[1].Service != "gone" || groups[2].Service != "redis" {
		t.Fatalf("bindings = %s", raw)
	}

	// Replacing reorders; the array order is the position.
	f.bind(t, "api", b, c)
	_, raw = f.call(t, http.MethodGet, f.bindingsPath(""), nil)
	_ = json.Unmarshal(raw, &groups)
	if !reflect.DeepEqual(groups[0].SetIDs, []int64{b.ID, c.ID}) {
		t.Fatalf("api bindings after replace = %v", groups[0].SetIDs)
	}
	sets := f.listSets(t)
	if len(sets[2].UsedBy) != 2 || sets[2].UsedBy[0].Service != "api" || sets[2].UsedBy[1].Service != "redis" {
		t.Fatalf("c.usedBy = %+v", sets[2].UsedBy)
	}

	f.bind(t, "api")
	_, raw = f.call(t, http.MethodGet, f.bindingsPath(""), nil)
	if strings.Contains(string(raw), `"api"`) {
		t.Fatalf("api still bound after []: %s", raw)
	}

	for name, body := range map[string]any{
		"duplicate":    map[string]any{"setIds": []int64{a.ID, a.ID}},
		"missing set":  map[string]any{"setIds": []int64{99999}},
		"no setIds":    map[string]any{},
		"unknown":      map[string]any{"setIds": []int64{}, "extra": 1},
		"string array": map[string]any{"setIds": []string{"x"}},
	} {
		if status, raw := f.call(t, http.MethodPut, f.bindingsPath("/api"), body); status != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (%s)", name, status, raw)
		}
	}
}

func TestCrossEnvironmentBindingIsRefused(t *testing.T) {
	f := newSetsFixture(t, setsYAML)
	local := f.createSet(t, "local")
	status, raw := f.call(t, http.MethodPost, projectEnvironmentsPath(f.projectID), map[string]any{"name": "Staging"})
	var staging environmentResponse
	if status != http.StatusCreated || json.Unmarshal(raw, &staging) != nil {
		t.Fatalf("create environment = %d %s", status, raw)
	}
	foreign := setsFixture{composeVariablesFixture{server: f.server, projectID: f.projectID, environmentID: staging.ID}}.createSet(t, "foreign")

	f.bind(t, "api", local)
	status, raw = f.call(t, http.MethodPut, f.bindingsPath("/api"), map[string]any{"setIds": []int64{local.ID, foreign.ID}})
	if status != http.StatusBadRequest {
		t.Fatalf("cross-environment bind status = %d, want 400 (%s)", status, raw)
	}
	// The refused request changed nothing.
	_, raw = f.call(t, http.MethodGet, f.bindingsPath(""), nil)
	if !strings.Contains(string(raw), `"setIds":[`+idString(local.ID)+`]`) {
		t.Fatalf("bindings after refused bind = %s", raw)
	}
}

func TestVariablesChangedAtMovesWithSetsAndBindings(t *testing.T) {
	f := newSetsFixture(t, setsYAML)
	other := createCompose(t, f.server, f.projectID, f.environmentID, "other")
	changedAt := func(composeID int64) *string {
		t.Helper()
		g := f.composeVariablesFixture
		g.composeID = composeID
		return g.variablesChangedAt(t)
	}
	reset := func() {
		t.Helper()
		if _, err := f.server.db.Exec("UPDATE composes SET variables_changed_at = 1"); err != nil {
			t.Fatal(err)
		}
	}
	const past = "1970-01-01T00:00:01Z"
	check := func(what string, wantThis, wantOther bool) {
		t.Helper()
		for composeID, want := range map[int64]bool{f.composeID: wantThis, other.ID: wantOther} {
			got := changedAt(composeID)
			if moved := got == nil || *got != past; moved != want {
				t.Errorf("%s: compose %d moved = %v, want %v", what, composeID, moved, want)
			}
		}
	}

	set := f.createSet(t, "stripe")
	f.bind(t, "api", set)
	reset()
	check("reset", false, false)

	// Binding stamps only that Compose.
	f.bind(t, "worker", set)
	check("bind", true, false)

	reset()
	f.bind(t, "worker", set) // no change at all
	check("identical rebind", false, false)

	// The set is bound only by f's Compose: entry writes stamp it; a rename does not (set files are named by ID).
	reset()
	entry := f.addEntry(t, set, "K", "v", false)
	check("add entry", true, false)
	reset()
	f.call(t, http.MethodPut, f.setsPath("/"+idString(set.ID)+"/entries/"+idString(entry.ID)), map[string]any{"key": "K", "value": "w"})
	check("update entry", true, false)
	reset()
	f.call(t, http.MethodPut, f.setsPath("/"+idString(set.ID)), map[string]any{"name": "billing"})
	check("rename", false, false)
	reset()
	f.call(t, http.MethodDelete, f.setsPath("/"+idString(set.ID)+"/entries/"+idString(entry.ID)), nil)
	check("delete entry", true, false)

	// Bound by both Composes: both are stamped.
	g := setsFixture{composeVariablesFixture{server: f.server, projectID: f.projectID, environmentID: f.environmentID, composeID: other.ID}}
	g.bind(t, "web", set)
	reset()
	f.addEntry(t, set, "K2", "v", false)
	check("entry with two binders", true, true)

	// A rejected write must not stamp.
	reset()
	f.call(t, http.MethodPost, f.setsPath("/"+idString(set.ID)+"/entries"), map[string]any{"key": "K2", "value": "dup"})
	check("rejected entry", false, false)
}

func TestDeployWritesSetFilesAndInjectsThemInOrder(t *testing.T) {
	yaml := "services:\n  api: {image: nginx}\n  worker:\n    image: busybox\n    env_file: ./own.env\n  cache:\n    image: redis\n    env_file: [./a.env, ./b.env]\n  redis: {image: redis}\n"
	f := newSetsFixture(t, yaml)
	first, second := f.createSet(t, "first"), f.createSet(t, "second")
	f.addEntry(t, first, "A", "1", false)
	f.addEntry(t, second, "B", "two words $x", true)
	f.bind(t, "api", second, first)
	f.bind(t, "worker", first)
	f.bind(t, "cache", second)

	d := f.deploy(t)
	envFile := d.envFile
	want := map[string][]string{
		"api":    {envFile, d.setPath(second), d.setPath(first)},
		"worker": {"./own.env", envFile, d.setPath(first)},
		"cache":  {"./a.env", "./b.env", envFile, d.setPath(second)},
		"redis":  {envFile},
	}
	if !reflect.DeepEqual(d.envFiles, want) {
		t.Fatalf("env_file lists =\n%v\nwant\n%v", d.envFiles, want)
	}
	for _, c := range []struct {
		set  setResponse
		want string
	}{{first, "A=\"1\"\n"}, {second, "B=\"two words $$x\"\n"}} {
		path, wantContent := d.setPath(c.set), c.want
		content, err := os.ReadFile(path)
		if err != nil || string(content) != wantContent {
			t.Fatalf("%s = %q (%v), want %q", path, content, err, wantContent)
		}
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", path, info.Mode().Perm())
		}
	}
	// Sets never reach the interpolation file.
	if content, _ := os.ReadFile(envFile); strings.Contains(string(content), "A=") || strings.Contains(string(content), "B=") {
		t.Errorf(".env holds set values: %s", content)
	}
	if len(d.warnings) != 0 {
		t.Errorf("warnings = %v, want none", d.warnings)
	}

	// Unbinding and redeploying removes the stale files and the references.
	f.bind(t, "api")
	f.bind(t, "worker")
	f.bind(t, "cache", second)
	d = f.deploy(t)
	if _, err := os.Stat(d.setPath(first)); !os.IsNotExist(err) {
		t.Errorf("stale set file stat err = %v, want it removed", err)
	}
	if _, err := os.Stat(d.setPath(second)); err != nil {
		t.Errorf("still-bound set file: %v", err)
	}
	if !reflect.DeepEqual(d.envFiles["api"], []string{envFile}) {
		t.Errorf("api env_file after unbind = %v", d.envFiles["api"])
	}

	f.bind(t, "cache")
	d = f.deploy(t)
	if _, err := os.Stat(filepath.Dir(d.setPath(second))); !os.IsNotExist(err) {
		t.Errorf("sets dir stat err = %v, want it removed when nothing is bound", err)
	}
}

func TestDeployFreshRewritesSetFiles(t *testing.T) {
	f := newSetsFixture(t, setsYAML)
	set := f.createSet(t, "stripe")
	f.addEntry(t, set, "K", "v", false)
	f.bind(t, "api", set)
	d := f.deploy(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(d.setPath(set)), "999.env"), []byte("OLD=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, raw := f.call(t, http.MethodPost, composeDeployFreshPath(f.projectID, f.environmentID, f.composeID), map[string]any{"confirmName": "web"})
	if status != http.StatusOK {
		t.Fatalf("deploy-fresh status = %d, body %s", status, raw)
	}
	entries, err := os.ReadDir(filepath.Dir(d.setPath(set)))
	if err != nil || len(entries) != 1 || entries[0].Name() != idString(set.ID)+".env" {
		t.Fatalf("sets dir after deploy-fresh = %v (%v)", entries, err)
	}
}

func TestVariableWarnings(t *testing.T) {
	yaml := "services:\n  api:\n    image: nginx:${TAG}\n    command: echo ${STRIPE_KEY} $$ESCAPED $LATE_ONE ${DB_URL:-x} ${ENV_VAR}\n"
	f := newSetsFixture(t, yaml)
	f.createEnvironmentVariable(t, "ENV_VAR", "1")
	stripe, db, dup := f.createSet(t, "stripe"), f.createSet(t, "db"), f.createSet(t, "dup")
	f.addEntry(t, stripe, "STRIPE_KEY", "k", true)
	f.addEntry(t, stripe, "ENV_VAR", "shadow", false) // also an Environment Variable: no hint
	f.addEntry(t, stripe, "ESCAPED", "e", false)      // $$ is a literal dollar
	f.addEntry(t, db, "DB_URL", "u", false)
	f.addEntry(t, db, "LATE_ONE", "l", false)
	f.addEntry(t, dup, "DB_URL", "other", false)
	f.addEntry(t, dup, "STRIPE_KEY", "other", false)
	f.bind(t, "api", stripe, db, dup)
	f.bind(t, "old-api", db)

	d := f.deploy(t)
	sort.Strings(d.warnings)
	want := []string{
		`${DB_URL} is only in variable set "db", which is not used for ${...} interpolation`,
		`${LATE_ONE} is only in variable set "db", which is not used for ${...} interpolation`,
		`${STRIPE_KEY} is only in variable set "dup", which is not used for ${...} interpolation`,
		`variable set "db" is bound to service "old-api", which the compose does not declare`,
		`variable sets "db" and "dup" both set "DB_URL" for service "api"; "dup" wins`,
		`variable sets "stripe" and "dup" both set "STRIPE_KEY" for service "api"; "dup" wins`,
	}
	sort.Strings(want)
	if !reflect.DeepEqual(d.warnings, want) {
		t.Fatalf("warnings =\n%q\nwant\n%q", d.warnings, want)
	}

	// Preview returns the same notes for the same draft.
	status, raw := f.call(t, http.MethodPost, composePath(f.projectID, f.environmentID, f.composeID)+"/preview", map[string]any{"yamlContent": yaml})
	var preview struct {
		Warnings []string `json:"warnings"`
	}
	if status != http.StatusOK || json.Unmarshal(raw, &preview) != nil {
		t.Fatalf("preview = %d %s", status, raw)
	}
	sort.Strings(preview.Warnings)
	if !reflect.DeepEqual(preview.Warnings, want) {
		t.Fatalf("preview warnings =\n%q\nwant\n%q", preview.Warnings, want)
	}
}

func TestNoVariableWarningsWithoutBindings(t *testing.T) {
	f := newSetsFixture(t, "services:\n  api:\n    image: nginx:${TAG}\n    command: echo $HOME ${ANY}\n")
	f.createSet(t, "unused") // a set nobody binds changes nothing
	f.addEntry(t, f.listSets(t)[0], "TAG", "x", false)
	d := f.deploy(t)
	if len(d.warnings) != 0 {
		t.Fatalf("warnings = %q, want none", d.warnings)
	}
	if !reflect.DeepEqual(d.envFiles["api"], []string{d.envFile}) {
		t.Fatalf("api env_file = %v", d.envFiles["api"])
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(d.envFile), "sets")); !os.IsNotExist(err) {
		t.Fatalf("sets dir stat err = %v, want absent for a Compose with no bindings", err)
	}
}

func TestPreviewReadsNoSetValues(t *testing.T) {
	f := newSetsFixture(t, setsYAML)
	set := f.createSet(t, "stripe")
	secret := "preview-set-secret-value"
	f.addEntry(t, set, "STRIPE_KEY", secret, true)
	f.bind(t, "api", set)
	status, raw := f.call(t, http.MethodPost, composePath(f.projectID, f.environmentID, f.composeID)+"/preview", map[string]any{"yamlContent": setsYAML + "  x:\n    image: ${STRIPE_KEY}\n"})
	if status != http.StatusOK || strings.Contains(string(raw), secret) {
		t.Fatalf("preview = %d %s", status, raw)
	}
	if !strings.Contains(string(raw), "sets/"+idString(set.ID)+".env") || !strings.Contains(string(raw), "STRIPE_KEY") {
		t.Fatalf("preview should show the set path and the hint, got %s", raw)
	}
}

type effectiveRow struct {
	Key       string          `json:"key"`
	Value     string          `json:"value"`
	Secret    bool            `json:"secret"`
	Source    json.RawMessage `json:"source"`
	Overrides []json.RawMessage
}

func (f setsFixture) effective(t *testing.T, service string) []effectiveRow {
	t.Helper()
	status, raw := f.call(t, http.MethodGet, composePath(f.projectID, f.environmentID, f.composeID)+"/services/"+service+"/effective-variables", nil)
	var rows []effectiveRow
	if status != http.StatusOK || json.Unmarshal(raw, &rows) != nil {
		t.Fatalf("effective %s = %d %s", service, status, raw)
	}
	return rows
}

// dotenv parses the files the deploy writes, which here hold plain values.
func dotenv(t *testing.T, path string) map[string]string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		if line == "" {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		values[key] = strings.Trim(value, `"`)
	}
	return values
}

func TestEffectiveVariablesMatchWhatTheDeployWrites(t *testing.T) {
	yaml := "services:\n  api:\n    image: nginx\n    environment:\n      FROM_YAML: x\n      LOG_LEVEL: warn\n  redis:\n    image: redis\n    environment: [CACHE=1, FLAG]\n"
	f := newSetsFixture(t, yaml)
	f.createEnvironmentVariable(t, "ENV_ONLY", "env")
	f.createEnvironmentVariable(t, "SHARED", "env-shared")
	f.createEnvironmentVariable(t, "LOG_LEVEL", "env-level")
	f.create(t, "SHARED", "compose-shared", false)
	f.create(t, "COMPOSE_ONLY", "compose", true)
	early, late := f.createSet(t, "early"), f.createSet(t, "late")
	f.addEntry(t, early, "SET_KEY", "early", false)
	f.addEntry(t, early, "LOG_LEVEL", "early-level", false)
	f.addEntry(t, late, "SET_KEY", "late", true)
	f.addEntry(t, late, "SHARED", "late-shared", false)
	f.bind(t, "api", early, late)

	d := f.deploy(t)

	for _, service := range []string{"api", "redis"} {
		// What a container gets: each env_file in order, later wins.
		got := map[string]string{}
		for _, path := range d.envFiles[service] {
			if !strings.HasPrefix(path, "/") {
				continue
			}
			for k, v := range dotenv(t, path) {
				got[k] = v
			}
		}
		fromAPI := map[string]string{}
		for _, row := range f.effective(t, service) {
			if string(row.Source) != `"yaml"` {
				fromAPI[row.Key] = row.Value
			}
		}
		if !reflect.DeepEqual(fromAPI, got) {
			t.Errorf("%s: effective variables = %v, deploy writes %v", service, fromAPI, got)
		}
	}

	api := map[string]effectiveRow{}
	var order []string
	for _, row := range f.effective(t, "api") {
		if _, seen := api[row.Key]; !seen {
			api[row.Key] = row
		}
		order = append(order, row.Key)
	}
	wantOrder := []string{"ENV_ONLY", "SHARED", "LOG_LEVEL", "COMPOSE_ONLY", "SET_KEY", "FROM_YAML", "LOG_LEVEL"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Errorf("api rows order = %v, want %v", order, wantOrder)
	}
	if row := api["ENV_ONLY"]; string(row.Source) != `"environment"` || len(row.Overrides) != 0 {
		t.Errorf("ENV_ONLY = %+v", row)
	}
	shared := api["SHARED"]
	if string(shared.Source) != `{"set":{"id":`+idString(late.ID)+`,"name":"late"}}` || shared.Value != "late-shared" ||
		len(shared.Overrides) != 2 || string(shared.Overrides[0]) != `"environment"` || string(shared.Overrides[1]) != `"compose"` {
		t.Errorf("SHARED = %+v (overrides %q)", shared, shared.Overrides)
	}
	if row := api["COMPOSE_ONLY"]; string(row.Source) != `"compose"` || !row.Secret {
		t.Errorf("COMPOSE_ONLY = %+v", row)
	}
	if row := api["SET_KEY"]; row.Value != "late" || !row.Secret || len(row.Overrides) != 1 {
		t.Errorf("SET_KEY = %+v", row)
	}
	// A key the YAML also sets is marked as overridden by it on the merged row,
	// and keeps its separate yaml row.
	if row := api["LOG_LEVEL"]; len(row.Overrides) != 2 || string(row.Overrides[0]) != `"environment"` || string(row.Overrides[1]) != `"yaml"` {
		t.Errorf("LOG_LEVEL overrides = %q, want environment then yaml", row.Overrides)
	}
	if row := api["SET_KEY"]; len(row.Overrides) != 1 {
		t.Errorf("SET_KEY overrides = %q, want none from yaml", row.Overrides)
	}
	if row := api["FROM_YAML"]; string(row.Source) != `"yaml"` || row.Value != "" {
		t.Errorf("FROM_YAML = %+v", row)
	}
	// An unbound Service sees neither set; its environment: list form is parsed.
	redis := f.effective(t, "redis")
	var keys []string
	for _, row := range redis {
		keys = append(keys, row.Key)
	}
	if !reflect.DeepEqual(keys, []string{"ENV_ONLY", "SHARED", "LOG_LEVEL", "COMPOSE_ONLY", "CACHE", "FLAG"}) {
		t.Errorf("redis rows = %v", keys)
	}
	// A Service the YAML does not declare still answers, with what would apply.
	if rows := f.effective(t, "nonexistent"); len(rows) != 4 {
		t.Errorf("rows for an undeclared service = %d, want the 4 shared ones", len(rows))
	}
	if status, _ := f.call(t, http.MethodGet, composePath(f.projectID, f.environmentID, 9999)+"/services/api/effective-variables", nil); status != http.StatusNotFound {
		t.Errorf("effective for a missing compose status = %d, want 404", status)
	}
}

func TestVariableSetsAPIDoesNotLogValues(t *testing.T) {
	f := newSetsFixture(t, setsYAML)
	var captured bytes.Buffer
	log.SetOutput(&captured)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	sentinel := "sentinel-set-value-supersecret"
	set := f.createSet(t, "stripe")
	entry := f.addEntry(t, set, "JWT_SECRET", sentinel, true)
	f.listSets(t)
	f.call(t, http.MethodPut, f.setsPath("/"+idString(set.ID)+"/entries/"+idString(entry.ID)), map[string]any{"key": "JWT_SECRET", "value": sentinel + "-2", "secret": true})
	f.call(t, http.MethodPost, f.setsPath("/"+idString(set.ID)+"/entries"), map[string]any{"key": "JWT_SECRET", "value": sentinel})
	f.call(t, http.MethodPost, f.setsPath("/"+idString(set.ID)+"/entries"), map[string]any{"key": "BAD-KEY", "value": sentinel})
	f.bind(t, "api", set)
	f.deploy(t)
	f.effective(t, "api")
	f.call(t, http.MethodDelete, f.setsPath("/"+idString(set.ID)), nil)

	if strings.Contains(captured.String(), sentinel) {
		t.Fatalf("log output contains a variable value:\n%s", captured.String())
	}
}
