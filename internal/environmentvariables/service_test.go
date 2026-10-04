package environmentvariables_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"relist-panel/internal/database"
	"relist-panel/internal/environmentvariables"
	"relist-panel/internal/migrations"
)

// variablePayload is the wire shape of one Environment Variable, as the
// screens consume it.
type variablePayload struct {
	ID            int64  `json:"id"`
	EnvironmentID int64  `json:"environmentId"`
	Key           string `json:"key"`
	Value         string `json:"value"`
	Secret        bool   `json:"secret"`
}

// openMigratedDB gives each test a real SQLite file with every embedded
// migration applied — the repository seam runs against the true schema.
func openMigratedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// openDBAtVersion builds an older world for the backfill test: the schema as
// migration `version` leaves it, so rows can exist before the secret column
// does. Version 9 is owned by a parallel feature and absent from this tree;
// stopping at 8 puts the database one migration before the secret flag.
func openDBAtVersion(t *testing.T, version int64) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { db.Close() })

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("configure goose SQLite dialect: %v", err)
	}
	if err := goose.UpTo(db, ".", version); err != nil {
		t.Fatalf("migrate up to version %d: %v", version, err)
	}
	return db
}

func seedEnvironment(t *testing.T, db *sql.DB) (projectID, environmentID int64) {
	t.Helper()
	now := time.Now().Unix()
	result, err := db.Exec("INSERT INTO projects (name, description, created_at, updated_at) VALUES ('Storefront', '', ?, ?)", now, now)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	projectID, err = result.LastInsertId()
	if err != nil {
		t.Fatalf("seed project id: %v", err)
	}
	result, err = db.Exec("INSERT INTO environments (project_id, name, created_at) VALUES (?, 'production', ?)", projectID, now)
	if err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	environmentID, err = result.LastInsertId()
	if err != nil {
		t.Fatalf("seed environment id: %v", err)
	}
	return projectID, environmentID
}

// variablesServer stands the Environment Variable API up over db exactly as
// httpapi wires it: the package's own handlers on their real routes, behind
// an httptest server, over a freshly seeded Environment. The returned paths
// are absolute URLs.
func variablesServer(t *testing.T, db *sql.DB) (variablesURL func() string, variableURL func(id int64) string) {
	t.Helper()
	projectID, environmentID := seedEnvironment(t, db)
	return variablesRoutes(t, db, projectID, environmentID)
}

// variablesRoutes is variablesServer over an Environment the caller already
// seeded (the backfill test builds its rows before the column exists).
func variablesRoutes(t *testing.T, db *sql.DB, projectID, environmentID int64) (variablesURL func() string, variableURL func(id int64) string) {
	t.Helper()
	mux := http.NewServeMux()
	environmentvariables.New(db).RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	base := server.URL + "/api/projects/" + strconv.FormatInt(projectID, 10) +
		"/environments/" + strconv.FormatInt(environmentID, 10) + "/variables"
	return func() string { return base },
		func(id int64) string { return base + "/" + strconv.FormatInt(id, 10) }
}

// requestVariable performs one API call and, when a body is expected, decodes
// it as one variable payload.
func requestVariable(t *testing.T, client *http.Client, method, url string, body any, wantStatus int) variablePayload {
	t.Helper()
	var payload variablePayload
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("build %s request: %v", method, err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("%s %s status = %d, want %d (body %s)", method, url, response.StatusCode, wantStatus, raw)
	}
	if wantStatus != http.StatusNoContent {
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatalf("decode %s %s response: %v", method, url, err)
		}
	}
	return payload
}

func requestList(t *testing.T, client *http.Client, url string) []variablePayload {
	t.Helper()
	response, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d", url, response.StatusCode, http.StatusOK)
	}
	var listed []variablePayload
	if err := json.NewDecoder(response.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	return listed
}

// The flag round-trips the whole seam: absent on create means plain, an
// explicit true or false persists through create and update, and the list
// payload carries it per variable.
func TestSecretFlagRoundTripsCreateUpdateList(t *testing.T) {
	variablesURL, variableURL := variablesServer(t, openMigratedDB(t))
	client := &http.Client{}

	absent := requestVariable(t, client, http.MethodPost, variablesURL(), map[string]any{
		"key":   "DATABASE_URL",
		"value": "postgres://production",
	}, http.StatusCreated)
	if absent.Secret {
		t.Error("created variable without secret = flagged secret, want plain (absent means false)")
	}

	flagged := requestVariable(t, client, http.MethodPost, variablesURL(), map[string]any{
		"key":    "JWT_SECRET",
		"value":  "hunter2",
		"secret": true,
	}, http.StatusCreated)
	if !flagged.Secret {
		t.Error("created variable with secret: true = plain, want secret")
	}

	// Toggling either way is the user's correction path.
	turnedOn := requestVariable(t, client, http.MethodPut, variableURL(absent.ID), map[string]any{
		"key":    "DATABASE_URL",
		"value":  "postgres://rotated",
		"secret": true,
	}, http.StatusOK)
	if !turnedOn.Secret {
		t.Error("updated variable with secret: true = plain, want secret")
	}

	turnedOff := requestVariable(t, client, http.MethodPut, variableURL(flagged.ID), map[string]any{
		"key":    "JWT_SECRET",
		"value":  "hunter3",
		"secret": false,
	}, http.StatusOK)
	if turnedOff.Secret {
		t.Error("updated variable with an explicit secret: false = secret, want plain")
	}

	listed := requestList(t, client, variablesURL())
	want := map[string]bool{"DATABASE_URL": true, "JWT_SECRET": false}
	if len(listed) != len(want) {
		t.Fatalf("list = %+v, want %d variables", listed, len(want))
	}
	for _, variable := range listed {
		if secret, ok := want[variable.Key]; !ok || secret != variable.Secret {
			t.Errorf("list %s secret = %v, want %v", variable.Key, variable.Secret, secret)
		}
	}
}

// Validation is unchanged by the flag: the key rule and the request shape
// answer exactly as before.
func TestValidationUnchangedWithSecretFlag(t *testing.T) {
	variablesURL, _ := variablesServer(t, openMigratedDB(t))
	client := &http.Client{}

	badKey := requestVariable(t, client, http.MethodPost, variablesURL(), map[string]any{
		"key":   "MY-KEY",
		"value": "v",
	}, http.StatusBadRequest)
	_ = badKey

	// The decoder still rejects unknown fields, so an old client cannot
	// smuggle a misspelled flag in silently.
	unknown := `{"key":"OK_KEY","value":"v","sercret":true}`
	response, err := client.Post(variablesURL(), "application/json", strings.NewReader(unknown))
	if err != nil {
		t.Fatalf("POST unknown field: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST with unknown field status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
}

// The one-time backfill: rows that predate the column come back flagged by
// the UI's key-shape heuristic — the same words SECRET_KEY_PATTERN matches,
// with `key` only on underscore boundaries — and nothing else.
func TestMigrationBackfillsSecretFromKeyShape(t *testing.T) {
	// The pre-flag world: version 8, before 00010 adds the column.
	db := openDBAtVersion(t, 8)
	projectID, environmentID := seedEnvironment(t, db)
	for _, key := range []string{
		"JWT_SECRET", "SMTP_PASSWORD", "API_KEY", "TOKEN_TTL", "PRIVATE_KEY_PATH",
		"HONEYPOT_KEY", "APIKEY_VAULT", "MONKEY", "MONKEY_PATCH", "DATABASE_URL", "KEYCLOAK_URL",
	} {
		if _, err := db.Exec("INSERT INTO environment_variables (environment_id, key, value) VALUES (?, ?, 'v')", environmentID, key); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}

	// Migrating applies 00010 across the deliberately skipped 00009 and runs
	// the backfill exactly once, inside the migration.
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate to the secret flag: %v", err)
	}

	variablesURL, _ := variablesRoutes(t, db, projectID, environmentID)
	listed := requestList(t, &http.Client{}, variablesURL())
	want := map[string]bool{
		"JWT_SECRET": true, "SMTP_PASSWORD": true, "API_KEY": true, "TOKEN_TTL": true,
		"PRIVATE_KEY_PATH": true, "HONEYPOT_KEY": true, "APIKEY_VAULT": true,
		"MONKEY": false, "MONKEY_PATCH": false, "DATABASE_URL": false, "KEYCLOAK_URL": false,
	}
	if len(listed) != len(want) {
		t.Fatalf("list = %+v, want %d variables", listed, len(want))
	}
	for _, variable := range listed {
		if secret, ok := want[variable.Key]; !ok || secret != variable.Secret {
			t.Errorf("backfilled %s secret = %v, want %v", variable.Key, variable.Secret, secret)
		}
	}
}

// A display tier, not a security boundary — but a display tier must not leak
// into the logs. Every variables endpoint runs with the standard logger
// captured; the values that crossed the wire must not cross into its output.
func TestVariablesAPIDoesNotLogValues(t *testing.T) {
	variablesURL, variableURL := variablesServer(t, openMigratedDB(t))
	client := &http.Client{}

	var captured bytes.Buffer
	log.SetOutput(&captured)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	sentinel := "sentinel-hunter-2-supersecret"
	created := requestVariable(t, client, http.MethodPost, variablesURL(), map[string]any{
		"key":    "JWT_SECRET",
		"value":  sentinel,
		"secret": true,
	}, http.StatusCreated)
	if created.ID == 0 {
		t.Fatal("create did not run; the log assertion below would pass vacuously")
	}
	requestList(t, client, variablesURL())
	requestVariable(t, client, http.MethodPut, variableURL(created.ID), map[string]any{
		"key":    "JWT_SECRET",
		"value":  sentinel + "-rotated",
		"secret": true,
	}, http.StatusOK)
	// Error paths included: a rejected body and a missing parent.
	requestVariable(t, client, http.MethodPost, variablesURL(), map[string]any{
		"key":   "MY-KEY",
		"value": sentinel,
	}, http.StatusBadRequest)
	response, err := client.Get(strings.Replace(variablesURL(), "/environments/", "/environments/9999/", 1))
	if err != nil {
		t.Fatalf("GET missing environment: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("GET missing environment status = %d, want %d", response.StatusCode, http.StatusNotFound)
	}

	if strings.Contains(captured.String(), sentinel) {
		t.Fatalf("log output contains a variable value:\n%s", captured.String())
	}
}
