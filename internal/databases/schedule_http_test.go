package databases

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestScheduleRoutes(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "18")

	w := a.do("GET", "/api/databases/1/schedule", "")
	var got struct {
		ScheduleJSON
		Next     *string `json:"next"`
		TimeZone string  `json:"timeZone"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.Enabled || got.Minute != 180 || !got.All || got.LocalKeepCount == nil || *got.LocalKeepCount != 7 ||
		got.RemoteKeepCount == nil || *got.RemoteKeepCount != 30 || got.Next != nil || got.TimeZone == "" {
		t.Fatalf("default = %d %s", w.Code, w.Body)
	}

	body := `{"enabled":true,"days":["thu","mon"],"minute":150,"all":false,"databases":["web","app"],"localKeepCount":4,"localKeepDays":null,"remoteKeepCount":null,"remoteKeepDays":9}`
	w = a.do("PUT", "/api/databases/1/schedule", body)
	if w.Code != 200 {
		t.Fatalf("PUT = %d %s", w.Code, w.Body)
	}
	w = a.do("GET", "/api/databases/1/schedule", "")
	got.Next = nil
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if !got.Enabled || strings.Join(got.Days, ",") != "mon,thu" || got.Minute != 150 || got.All || strings.Join(got.Databases, ",") != "app,web" ||
		*got.LocalKeepCount != 4 || got.LocalKeepDays != nil || got.RemoteKeepCount != nil || *got.RemoteKeepDays != 9 || got.Next == nil || got.LastRunAt == nil {
		t.Fatalf("round trip = %s", w.Body)
	}
	if a.last == nil {
		t.Fatal("no audit event")
	}
}

func TestScheduleRouteErrors(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "18")
	a.seed(t, "Cache", "redis", "8")
	put := func(id, body string) *http.Response {
		w := a.do("PUT", "/api/databases/"+id+"/schedule", body)
		return w.Result()
	}
	base := func(edit string) string {
		return `{"enabled":true,"days":["daily"],"minute":180,"all":true,"databases":[]` + edit + `}`
	}
	for _, c := range []struct {
		name, id, body string
		code           int
		msg            string
	}{
		{"unknown day", "1", `{"enabled":true,"days":["sat","xyz"],"minute":1,"all":true}`, 422, "days must be daily or weekdays mon to sun"},
		{"no days", "1", `{"enabled":true,"days":[],"minute":1,"all":true}`, 422, "choose Daily or at least one weekday"},
		{"minute high", "1", `{"enabled":true,"days":["daily"],"minute":1440,"all":true}`, 422, "the time must be between 00:00 and 23:59"},
		{"minute low", "1", `{"enabled":true,"days":["daily"],"minute":-1,"all":true}`, 422, "the time must be between 00:00 and 23:59"},
		{"keep zero", "1", base(`,"localKeepCount":0`), 422, "keep at least 1"},
		{"keep negative days", "1", base(`,"remoteKeepDays":-3`), 422, "keep at least 1"},
		{"no scope", "1", `{"enabled":true,"days":["daily"],"minute":1,"all":false,"databases":[]}`, 400, ErrNoScope.Error()},
		{"redis", "2", base(``), 400, ErrNoBackups.Error()},
		{"missing", "9", base(``), 404, "Database not found"},
	} {
		resp := put(c.id, c.body)
		var out map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if resp.StatusCode != c.code || out["error"] != c.msg {
			t.Errorf("%s: %d %q, want %d %q", c.name, resp.StatusCode, out["error"], c.code, c.msg)
		}
	}
	if w := a.do("GET", "/api/databases/2/schedule", ""); w.Code != 400 {
		t.Errorf("GET redis = %d", w.Code)
	}
}

func TestPinRoute(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "18")
	manual := a.manualBackup(t, 1)
	sched, err := a.store.InsertBackup(context.Background(), Backup{DatabaseID: 1, Trigger: TriggerScheduled, LogicalName: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if w := a.do("PUT", "/api/databases/1/backups/"+itoa(manual.ID)+"/pin", `{"pinned":true}`); w.Code != 409 {
		t.Fatalf("pin manual = %d %s", w.Code, w.Body)
	}
	if w := a.do("PUT", "/api/databases/1/backups/"+itoa(sched.ID)+"/pin", `{"pinned":true}`); w.Code != 204 {
		t.Fatalf("pin scheduled = %d %s", w.Code, w.Body)
	}
	w := a.do("GET", "/api/databases/1/backups", "")
	var list []BackupJSON
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	pinned := 0
	for _, b := range list {
		if b.Pinned {
			pinned++
			if b.ID != sched.ID {
				t.Fatalf("pinned %d", b.ID)
			}
		}
	}
	if pinned != 1 {
		t.Fatalf("list = %s", w.Body)
	}
	if w := a.do("PUT", "/api/databases/1/backups/"+itoa(sched.ID)+"/pin", `{"pinned":false}`); w.Code != 204 {
		t.Fatalf("unpin = %d", w.Code)
	}
}

func TestRemoteOnlyBackupRoutes(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "18")
	b := a.manualBackup(t, 1)
	_ = a.store.SetLocalRemoved(context.Background(), b.ID, a.svc.d.Now())
	if w := a.do("GET", "/api/databases/1/backups/"+itoa(b.ID)+"/download", ""); w.Code != 409 {
		t.Fatalf("download = %d %s", w.Code, w.Body)
	}
	if w := a.do("POST", "/api/databases/1/restores", `{"backupId":`+itoa(b.ID)+`,"target":"app","confirm":"Shop"}`); w.Code != 409 {
		t.Fatalf("restore = %d %s", w.Code, w.Body)
	}
	w := a.do("GET", "/api/databases/1/backups", "")
	if !strings.Contains(w.Body.String(), `"localRemoved":true`) {
		t.Fatalf("list = %s", w.Body)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
