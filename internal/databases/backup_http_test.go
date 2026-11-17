package databases

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"relist-panel/internal/agefile"
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
	a.dumps("pg_dump -U postgres", "SELECT 1;\n")

	w := a.do("POST", "/api/databases/1/backups", `{"all":true}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("POST = %d %s", w.Code, w.Body)
	}
	var created []BackupJSON
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if len(created) != 1 || created[0].ID != 1 || created[0].Status != JobRunning || created[0].Database != "app" || created[0].Trigger != "manual" {
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
	if w := a.do("POST", "/api/databases/1/backups", `{"databases":["nope"]}`); w.Code != 400 || !strings.Contains(w.Body.String(), "no such database: nope") {
		t.Fatalf("unknown logical = %d %s", w.Code, w.Body)
	}
	if w := a.do("POST", "/api/databases/1/backups", `{}`); w.Code != 400 {
		t.Fatalf("empty request = %d %s", w.Code, w.Body)
	}
	if w := a.do("POST", "/api/databases/1/backups", `{"databases":["-x"]}`); w.Code != 400 || !strings.Contains(w.Body.String(), "starting with -") {
		t.Fatalf("unsafe name = %d %s", w.Code, w.Body)
	}

	a.do("POST", "/api/databases/1/stop", "")
	if w := a.do("POST", "/api/databases/1/backups", `{"all":true}`); w.Code != 409 {
		t.Fatalf("stopped = %d %s", w.Code, w.Body)
	}
	if w := a.do("GET", "/api/databases/1/logical-databases", ""); w.Code != 409 {
		t.Fatalf("logical while stopped = %d", w.Code)
	}
}

func TestBackupRouteNothingToBackUp(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "mysql", "8.4")
	a.dumps("mysql -uroot -N -e SHOW DATABASES", "information_schema\nmysql\n")
	if w := a.do("POST", "/api/databases/1/backups", `{"all":true}`); w.Code != 400 || !strings.Contains(w.Body.String(), "there are no databases to back up") {
		t.Fatalf("nothing = %d %s", w.Code, w.Body)
	}
}

func TestBackupRoutesRedis400(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Cache", "redis", "8")
	for _, c := range [][2]string{
		{"POST", "/api/databases/1/backups"}, {"GET", "/api/databases/1/logical-databases"},
		{"GET", "/api/databases/1/backups"}, {"GET", "/api/databases/1/restores"},
		{"GET", "/api/databases/1/backups/1/download"}, {"DELETE", "/api/databases/1/backups/1"},
		{"POST", "/api/databases/1/restores"}, {"POST", "/api/databases/1/restores/upload?confirm=Cache&name=a.sql&target=x"},
	} {
		if w := a.do(c[0], c[1], `{"all":true,"backupId":1,"target":"x","confirm":"Cache"}`); w.Code != 400 {
			t.Errorf("%s %s = %d %s", c[0], c[1], w.Code, w.Body)
		}
	}
}

func TestRestoreUploadRoute(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "18")
	a.dumps("pg_dump -U postgres", "PRE;\n")
	a.adminSQL(pgAdmin, "postgres\n", nil)
	got := a.captureRestore(pgRestore, "")
	base := "/api/databases/1/restores/upload?name=a.sql"

	if w := a.do("POST", base+"&confirm=nope&target=app", "SELECT 1;"); w.Code != 400 {
		t.Fatalf("wrong confirm = %d %s", w.Code, w.Body)
	}
	if w := a.do("POST", base+"&confirm=Shop", "SELECT 1;"); w.Code != 400 || !strings.Contains(w.Body.String(), "pick a database to restore into") {
		t.Fatalf("no target = %d %s", w.Code, w.Body)
	}
	if w := a.do("POST", base+"&confirm=Shop&target=template1", "SELECT 1;"); w.Code != 400 {
		t.Fatalf("template1 = %d %s", w.Code, w.Body)
	}
	if w := a.do("POST", base+"&confirm=Shop&target=app", "CREATE ROLE x;\n"); w.Code != 400 || !strings.Contains(w.Body.String(), `line 1: \"CREATE ROLE x;\" is not allowed in an upload`) {
		t.Fatalf("whole-instance = %d %s", w.Code, w.Body)
	}
	if w := a.do("POST", base+"&confirm=Shop&target=app", "\x1f\x8bnot gzip"); w.Code != 400 {
		t.Fatalf("bad gzip = %d %s", w.Code, w.Body)
	}
	if len(*got) != 0 {
		t.Fatal("a refused upload still ran the restore")
	}
	w := a.do("POST", base+"&confirm=Shop&target=app", "SELECT 1;")
	if w.Code != http.StatusAccepted {
		t.Fatalf("upload = %d %s", w.Code, w.Body)
	}
	var rs RestoreJSON
	_ = json.Unmarshal(w.Body.Bytes(), &rs)
	if rs.Source != "upload" || rs.UploadName != "a.sql" || rs.Target != "app" || len(*got) != 1 || (*got)[0] != "SELECT 1;" {
		t.Fatalf("restore = %+v, stdin %q", rs, *got)
	}
	w = a.do("GET", "/api/databases/1/restores", "")
	var list []RestoreJSON
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Status != JobSucceeded || list[0].PreBackupID == nil || list[0].Target != "app" {
		t.Fatalf("restores = %s", w.Body)
	}
}

func TestRestoreFromBackupRoute(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "18")
	a.dumps("pg_dump -U postgres", "PRE;\n")
	a.do("POST", "/api/databases/1/backups", `{"all":true}`)
	a.adminSQL(pgAdmin, "postgres\n", nil)
	a.captureRestore(pgRestore, "")
	if w := a.do("POST", "/api/databases/1/restores", `{"backupId":1,"target":"app","confirm":"x"}`); w.Code != 400 {
		t.Fatalf("wrong confirm = %d", w.Code)
	}
	if w := a.do("POST", "/api/databases/1/restores", `{"backupId":1,"confirm":"Shop"}`); w.Code != 400 || !strings.Contains(w.Body.String(), "pick a database to restore into") {
		t.Fatalf("no target = %d %s", w.Code, w.Body)
	}
	if w := a.do("POST", "/api/databases/1/restores", `{"backupId":99,"target":"app","confirm":"Shop"}`); w.Code != 404 {
		t.Fatalf("unknown backup = %d", w.Code)
	}
	if w := a.do("POST", "/api/databases/1/restores", `{"backupId":1,"target":"app","confirm":"Shop"}`); w.Code != http.StatusAccepted {
		t.Fatalf("restore = %d %s", w.Code, w.Body)
	}
	// A Backup of the earlier, several-databases format is not restorable.
	old, _ := a.store.InsertBackup(context.Background(), Backup{DatabaseID: 1, Trigger: TriggerManual})
	_ = os.MkdirAll(a.backupsDir(1), 0o700)
	_ = os.WriteFile(filepath.Join(a.backupsDir(1), "old.sql.gz"), []byte("x"), 0o600)
	_ = a.store.FinishBackup(context.Background(), old.ID, Backup{Status: JobSucceeded, FileName: "old.sql.gz", SHA256: "x"})
	if w := a.do("POST", "/api/databases/1/restores", `{"backupId":`+fmt.Sprint(old.ID)+`,"target":"app","confirm":"Shop"}`); w.Code != 400 || !strings.Contains(w.Body.String(), "an earlier format") {
		t.Fatalf("old format = %d %s", w.Code, w.Body)
	}
}

func TestDeleteBodyDeleteBackups(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "18")
	a.dumps("pg_dump -U postgres", "X;\n")
	a.do("POST", "/api/databases/1/backups", `{"all":true}`)
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
	a.do("POST", "/api/databases/2/backups", `{"all":true}`)
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
	a.dumps("pg_dump -U postgres", "X;\n")
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
	a.dumps("pg_dump -U postgres", "X;\n")
	a.do("POST", "/api/databases/1/backups", `{"all":true}`)
	if w := a.do("GET", "/api/databases/1/backups/1/download", ""); w.Code != 200 {
		t.Fatalf("download = %d", w.Code)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := a.last.Event(); e.Target != "Shop" || e.Detail["backup"] != int64(1) {
		t.Fatalf("audit event = %+v", e)
	}
}

// The passphrase travels in the X-Passphrase header, never the query string.
func TestRestoreUploadPassphraseHeader(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "18")
	a.dumps("pg_dump -U postgres", "PRE;\n")
	a.adminSQL(pgAdmin, "postgres\n", nil)
	got := a.captureRestore(pgRestore, "")
	var buf bytes.Buffer
	if err := agefile.Encrypt(&buf, agePass, strings.NewReader("SELECT 1;\n")); err != nil {
		t.Fatal(err)
	}
	enc := buf.Bytes()
	base := "/api/databases/1/restores/upload?name=a.sql.age&confirm=Shop&target=app"
	// Without the passphrase: a 422 that names it.
	req := httptest.NewRequest("POST", base, bytes.NewReader(enc))
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, req)
	a.svc.Wait()
	if w.Code != 422 || !strings.Contains(w.Body.String(), "enter its passphrase") {
		t.Fatalf("missing passphrase = %d %s", w.Code, w.Body)
	}
	// A wrong one: 422 with the message.
	req = httptest.NewRequest("POST", base, bytes.NewReader(enc))
	req.Header.Set("X-Passphrase", "not-the-passphrase-00000000")
	w = httptest.NewRecorder()
	a.h.ServeHTTP(w, req)
	a.svc.Wait()
	if w.Code != 422 || !strings.Contains(w.Body.String(), "wrong passphrase") {
		t.Fatalf("wrong passphrase = %d %s", w.Code, w.Body)
	}
	// The right one reaches the service and restores.
	req = httptest.NewRequest("POST", base, bytes.NewReader(enc))
	req.Header.Set("X-Passphrase", agePass)
	w = httptest.NewRecorder()
	a.h.ServeHTTP(w, req)
	a.svc.Wait()
	if w.Code != http.StatusAccepted || len(*got) != 1 || (*got)[0] != "SELECT 1;\n" {
		t.Fatalf("restore = %d %s, stdin %q", w.Code, w.Body, *got)
	}
}
