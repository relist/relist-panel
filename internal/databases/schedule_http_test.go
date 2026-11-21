package databases

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"relist-panel/internal/backupdest"
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

func TestScheduleRefusesUntestedSFTP(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "18")
	a.withDestination(t)
	body := `{"enabled":true,"days":["daily"],"minute":180,"all":true,"databases":[],"destinationId":1}`
	// An S3 destination that was never tested saves, as before.
	if w := a.do("PUT", "/api/databases/1/schedule", body); w.Code != 200 {
		t.Fatalf("s3 = %d %s", w.Code, w.Body)
	}
	sftp := backupdest.Destination{ID: 1, Name: "box", Type: backupdest.TypeSFTP,
		SFTP: backupdest.SFTPConfig{Host: "box", Port: 22, User: "u", Auth: backupdest.AuthPassword}}
	a.svc.d.Destinations = fakeOpener{remote: newFakeRemote(), dest: &sftp}
	w := a.do("PUT", "/api/databases/1/schedule", body)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "test the Backup destination first: it has no trusted host key") {
		t.Fatalf("untested sftp = %d %s", w.Code, w.Body)
	}
	sftp.HostKey = "ssh-ed25519 AAAA"
	a.svc.d.Destinations = fakeOpener{remote: newFakeRemote(), dest: &sftp}
	if w := a.do("PUT", "/api/databases/1/schedule", body); w.Code != 200 {
		t.Fatalf("tested sftp = %d %s", w.Code, w.Body)
	}
}

func TestScheduleGFSAPI(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "18")
	get := func() ScheduleJSON {
		var got ScheduleJSON
		w := a.do("GET", "/api/databases/1/schedule", "")
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		return got
	}
	// A stored Simple schedule answers mode "simple".
	if w := a.do("PUT", "/api/databases/1/schedule", `{"days":["daily"],"minute":180,"all":true,"localKeepCount":7}`); w.Code != 200 {
		t.Fatalf("simple PUT = %d %s", w.Code, w.Body)
	}
	if got := get(); got.LocalMode != "simple" || got.RemoteMode != "simple" {
		t.Fatalf("modes = %q / %q, want simple", got.LocalMode, got.RemoteMode)
	}
	// A GFS set round-trips its tiers, and localKeepDays is cleared.
	body := `{"days":["daily"],"minute":180,"all":true,"localMode":"gfs","localKeepCount":3,"localKeepDays":9,"localGfsDays":7,"localGfsWeeks":4,"localGfsMonths":12,"localGfsYears":2}`
	if w := a.do("PUT", "/api/databases/1/schedule", body); w.Code != 200 {
		t.Fatalf("gfs PUT = %d %s", w.Code, w.Body)
	}
	got := get()
	if got.LocalMode != "gfs" || got.LocalGfsDays == nil || *got.LocalGfsDays != 7 || got.LocalGfsWeeks == nil || *got.LocalGfsWeeks != 4 ||
		got.LocalGfsMonths == nil || *got.LocalGfsMonths != 12 || got.LocalGfsYears == nil || *got.LocalGfsYears != 2 || *got.LocalKeepCount != 3 {
		t.Fatalf("gfs round trip = %+v", got)
	}
	if got.LocalKeepDays != nil {
		t.Fatalf("localKeepDays = %d, want cleared on a GFS save", *got.LocalKeepDays)
	}
	// A Simple save clears the GFS tiers.
	if w := a.do("PUT", "/api/databases/1/schedule", `{"days":["daily"],"minute":180,"all":true,"localMode":"simple","localKeepCount":7,"localGfsDays":7}`); w.Code != 200 {
		t.Fatalf("back to simple = %d %s", w.Code, w.Body)
	}
	if got := get(); got.LocalGfsDays != nil {
		t.Fatalf("localGfsDays = %d, want cleared on a Simple save", *got.LocalGfsDays)
	}
	// Review Focus 2: GFS with nothing set would delete everything but the
	// newest success; it is refused.
	w := a.do("PUT", "/api/databases/1/schedule", `{"days":["daily"],"minute":180,"all":true,"remoteMode":"gfs"}`)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "keep at least one tier") {
		t.Fatalf("empty gfs = %d %s", w.Code, w.Body)
	}
	// An unknown mode is refused.
	w = a.do("PUT", "/api/databases/1/schedule", `{"days":["daily"],"minute":180,"all":true,"localMode":"x","localKeepCount":1}`)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "rotation mode must be simple or gfs") {
		t.Fatalf("bad mode = %d %s", w.Code, w.Body)
	}
	// Tier values below 1 are refused like the Simple ones.
	w = a.do("PUT", "/api/databases/1/schedule", `{"days":["daily"],"minute":180,"all":true,"localMode":"gfs","localGfsWeeks":0}`)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "keep at least 1") {
		t.Fatalf("zero tier = %d %s", w.Code, w.Body)
	}
}
