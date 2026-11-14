package databases

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func (a *apiRig) seed(t *testing.T, name, engine, version string) {
	t.Helper()
	if w := a.do("POST", "/api/databases", `{"name":"`+name+`","engine":"`+engine+`","version":"`+version+`"}`); w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
}

func TestBackupRoutes(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "18")
	a.dumps("pg_dumpall", "SELECT 1;\n")

	w := a.do("POST", "/api/databases/1/backups", `{"scope":{"all":true}}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("POST = %d %s", w.Code, w.Body)
	}
	var created BackupJSON
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID != 1 || created.Status != JobRunning || !created.Scope.All || created.Trigger != "manual" {
		t.Fatalf("created = %+v", created)
	}

	w = a.do("GET", "/api/databases/1/backups", "")
	var list []BackupJSON
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list) != 1 || list[0].Status != JobSucceeded || list[0].Size == 0 || list[0].SHA256 == "" {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}

	w = a.do("GET", "/api/databases/1/backups/1/download", "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/gzip" {
		t.Fatalf("download = %d %v", w.Code, w.Header())
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="shop-`) || !strings.HasSuffix(cd, `.sql.gz"`) {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	zr, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(zr); string(got) != "SELECT 1;\n" {
		t.Fatalf("body = %q", got)
	}

	if w := a.do("DELETE", "/api/databases/1/backups/1", ""); w.Code != 204 {
		t.Fatalf("DELETE = %d %s", w.Code, w.Body)
	}
	if w := a.do("GET", "/api/databases/1/backups/1/download", ""); w.Code != 404 {
		t.Fatalf("download after delete = %d", w.Code)
	}
	if w := a.do("GET", "/api/databases/9/backups", ""); w.Code != 404 {
		t.Fatalf("unknown database = %d", w.Code)
	}
}

func TestBackupRouteErrors(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "18")
	a.dumps("psql -U postgres -At", "a\n")
	if w := a.do("POST", "/api/databases/1/backups", `{"scope":{"databases":["nope"]}}`); w.Code != 400 || !strings.Contains(w.Body.String(), "no such database: nope") {
		t.Fatalf("unknown logical = %d %s", w.Code, w.Body)
	}
	if w := a.do("POST", "/api/databases/1/backups", `{"scope":{}}`); w.Code != 400 {
		t.Fatalf("empty scope = %d %s", w.Code, w.Body)
	}
	a.do("POST", "/api/databases/1/stop", "")
	if w := a.do("POST", "/api/databases/1/backups", `{"scope":{"all":true}}`); w.Code != 409 {
		t.Fatalf("stopped = %d %s", w.Code, w.Body)
	}
	if w := a.do("GET", "/api/databases/1/logical-databases", ""); w.Code != 409 {
		t.Fatalf("logical while stopped = %d", w.Code)
	}
}

func TestBackupRoutesRedis400(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Cache", "redis", "8")
	for _, c := range [][2]string{
		{"POST", "/api/databases/1/backups"}, {"GET", "/api/databases/1/logical-databases"},
		{"POST", "/api/databases/1/restores"}, {"POST", "/api/databases/1/restores/upload?confirm=Cache&name=a.sql"},
	} {
		if w := a.do(c[0], c[1], `{"scope":{"all":true},"backupId":1,"confirm":"Cache"}`); w.Code != 400 {
			t.Errorf("%s %s = %d %s", c[0], c[1], w.Code, w.Body)
		}
	}
}

func TestRestoreUploadRoute(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "18")
	a.dumps("pg_dumpall", "PRE;\n")
	got := a.captureRestore("psql -U postgres -d template1 -v", "")

	if w := a.do("POST", "/api/databases/1/restores/upload?confirm=nope&name=a.sql", "SELECT 1;"); w.Code != 400 {
		t.Fatalf("wrong confirm = %d %s", w.Code, w.Body)
	}
	if len(*got) != 0 {
		t.Fatal("a wrong confirm still ran the restore")
	}
	w := a.do("POST", "/api/databases/1/restores/upload?confirm=Shop&name=a.sql", "SELECT 1;")
	if w.Code != http.StatusAccepted {
		t.Fatalf("upload = %d %s", w.Code, w.Body)
	}
	var rs RestoreJSON
	_ = json.Unmarshal(w.Body.Bytes(), &rs)
	if rs.Source != "upload" || rs.UploadName != "a.sql" || len(*got) != 1 || (*got)[0] != "SELECT 1;" {
		t.Fatalf("restore = %+v, stdin %q", rs, *got)
	}
	w = a.do("GET", "/api/databases/1/restores", "")
	var list []RestoreJSON
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Status != JobSucceeded || list[0].PreBackupID == nil {
		t.Fatalf("restores = %s", w.Body)
	}
}

func TestRestoreFromBackupRoute(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "18")
	a.dumps("pg_dumpall", "PRE;\n")
	a.do("POST", "/api/databases/1/backups", `{"scope":{"all":true}}`)
	a.captureRestore("psql -U postgres -d template1 -v", "")
	if w := a.do("POST", "/api/databases/1/restores", `{"backupId":1,"confirm":"x"}`); w.Code != 400 {
		t.Fatalf("wrong confirm = %d", w.Code)
	}
	if w := a.do("POST", "/api/databases/1/restores", `{"backupId":99,"confirm":"Shop"}`); w.Code != 404 {
		t.Fatalf("unknown backup = %d", w.Code)
	}
	if w := a.do("POST", "/api/databases/1/restores", `{"backupId":1,"confirm":"Shop"}`); w.Code != http.StatusAccepted {
		t.Fatalf("restore = %d %s", w.Code, w.Body)
	}
}

func TestDeleteBodyDeleteBackups(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "18")
	a.dumps("pg_dumpall", "X;\n")
	a.do("POST", "/api/databases/1/backups", `{"scope":{"all":true}}`)
	if w := a.do("DELETE", "/api/databases/1", `{"confirm":"Shop"}`); w.Code != 204 {
		t.Fatalf("delete = %d %s", w.Code, w.Body)
	}
	w := a.do("GET", "/api/databases/1", "")
	var d DatabaseJSON
	_ = json.Unmarshal(w.Body.Bytes(), &d)
	if w.Code != 200 || !d.Deleted || d.Status != "deleted" || d.Name != "Shop (deleted #1)" {
		t.Fatalf("tombstone = %d %s", w.Code, w.Body)
	}
	if w := a.do("GET", "/api/databases/1/credentials", ""); w.Code != 404 {
		t.Fatalf("credentials of a tombstone = %d", w.Code)
	}
	if w := a.do("GET", "/api/databases/1/backups", ""); w.Code != 200 {
		t.Fatalf("tombstone backups = %d", w.Code)
	}
	a.seed(t, "Other", "postgres", "17")
	a.do("POST", "/api/databases/2/backups", `{"scope":{"all":true}}`)
	if w := a.do("DELETE", "/api/databases/2", `{"confirm":"Other","deleteBackups":true}`); w.Code != 204 {
		t.Fatalf("delete = %d", w.Code)
	}
	if w := a.do("GET", "/api/databases/2", ""); w.Code != 404 {
		t.Fatalf("deleteBackups kept the row: %d", w.Code)
	}
}

func TestUpdateImageBackFirstRoute(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "18")
	a.dumps("pg_dumpall", "X;\n")
	if w := a.do("POST", "/api/databases/1/update-image", ""); w.Code != 204 {
		t.Fatalf("no body = %d %s", w.Code, w.Body)
	}
	if w := a.do("POST", "/api/databases/1/update-image", `{"backupFirst":false}`); w.Code != 204 {
		t.Fatalf("false = %d %s", w.Code, w.Body)
	}
	if w := a.do("POST", "/api/databases/1/update-image", `{"backupFirst":true}`); w.Code != 202 {
		t.Fatalf("true = %d %s", w.Code, w.Body)
	}
	a.do("POST", "/api/databases/1/stop", "")
	if w := a.do("POST", "/api/databases/1/update-image", `{"backupFirst":true}`); w.Code != 409 {
		t.Fatalf("stopped = %d %s", w.Code, w.Body)
	}
}

func TestDownloadAudited(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "18")
	a.dumps("pg_dumpall", "X;\n")
	a.do("POST", "/api/databases/1/backups", `{"scope":{"all":true}}`)
	if w := a.do("GET", "/api/databases/1/backups/1/download", ""); w.Code != 200 {
		t.Fatalf("download = %d", w.Code)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := a.last.Event(); e.Target != "Shop" || e.Detail["backup"] != int64(1) {
		t.Fatalf("audit event = %+v", e)
	}
}
