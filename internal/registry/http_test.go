package registry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type apiFixture struct {
	*svcFixture
	mux      *http.ServeMux
	deleted  *[]string
	deployed map[string][]User
	running  map[string][]User
	gate     *fakeGate
	cleaner  *Cleaner
	requests *atomic.Int64 // requests the API sent the registry
	// fail, when set, answers 500 to the requests it matches.
	fail atomic.Pointer[func(*http.Request) bool]
}

type countingTransport struct {
	next http.RoundTripper
	n    *atomic.Int64
	fail *atomic.Pointer[func(*http.Request) bool]
}

func (c countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	if c.fail != nil {
		if fail := c.fail.Load(); fail != nil && (*fail)(r) {
			return &http.Response{StatusCode: 500, Body: http.NoBody, Header: http.Header{}, Request: r}, nil
		}
	}
	return c.next.RoundTrip(r)
}

func newAPI(t *testing.T) *apiFixture {
	t.Helper()
	f := &apiFixture{svcFixture: newService(t), deployed: map[string][]User{}, running: map[string][]User{}}
	f.enable(t)
	// The fake registry answers the system login "sys".
	st, _ := f.store.Settings(context.Background())
	st.SystemSecret = "sys"
	_ = f.store.SaveSettings(context.Background(), st)
	client, deleted := fakeRegistry(t)
	f.deleted = deleted
	f.requests = &atomic.Int64{}
	client.HTTP = &http.Client{Transport: countingTransport{next: client.HTTP.Transport, n: f.requests, fail: &f.fail}}
	f.mux = http.NewServeMux()
	f.gate = &fakeGate{}
	f.cleaner = NewCleaner(CleanerDeps{
		Service: f.svc, Gate: f.gate, Poll: time.Millisecond, GCPoll: time.Millisecond,
		Registry: func(st Settings) RegistryAPI {
			return &Client{Base: client.Base, User: SystemUser, Secret: st.SystemSecret, HTTP: client.HTTP}
		},
	})
	NewAPI(APIDeps{
		Cleaner:      f.cleaner,
		Service:      f.svc,
		RegistryBase: client.Base,
		HTTP:         client.HTTP,
		DeployedRefs: func(context.Context) (map[string][]User, error) { return f.deployed, nil },
		RunningRefs:  func(context.Context) (map[string][]User, error) { return f.running, nil },
		ReadTimeout:  func(context.Context) (string, error) { return "3600s", nil },
	}).RegisterRoutes(f.mux)
	return f
}

func (f *apiFixture) do(method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestGetRegistrySettings(t *testing.T) {
	f := newAPI(t)
	f.host.volume = 2468
	rec := f.do("GET", "/api/registry", "")
	var got struct {
		Settings struct {
			Enabled  bool   `json:"enabled"`
			Hostname string `json:"hostname"`
			TLS      string `json:"tls"`
		} `json:"settings"`
		Status        string   `json:"status"`
		DiskBytes     int64    `json:"diskBytes"`
		ReadTimeout   string   `json:"readTimeout"`
		ComposesUsing []string `json:"composesUsing"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("get = %d %s", rec.Code, rec.Body)
	}
	if !got.Settings.Enabled || got.Settings.Hostname != "registry.example.com" || got.Status != StatusRunning || got.DiskBytes != 2468 || got.ReadTimeout != "3600s" || got.ComposesUsing == nil {
		t.Fatalf("got %+v", got)
	}
	if strings.Contains(rec.Body.String(), "sys") || strings.Contains(strings.ToLower(rec.Body.String()), "secret") {
		t.Fatalf("settings leak the system login: %s", rec.Body)
	}
}

func TestPutRegistryConflict(t *testing.T) {
	f := newAPI(t)
	rec := f.do("PUT", "/api/registry", `{"hostname":"panel.example.com","tls":"letsencrypt"}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "panel.example.com is the panel's own domain") {
		t.Fatalf("put = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("PUT", "/api/registry", `{"hostname":"https://x","tls":"letsencrypt"}`); rec.Code != 422 {
		t.Fatalf("bad hostname = %d", rec.Code)
	}
}

func TestDisableReturnsWarnings(t *testing.T) {
	f := newAPI(t)
	f.using = []string{"shop"}
	rec := f.do("POST", "/api/registry/disable", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"warnings":["shop"]`) {
		t.Fatalf("disable = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("DELETE", "/api/registry/data", `{"confirm":"nope"}`); rec.Code != 422 {
		t.Fatalf("wrong confirm = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("DELETE", "/api/registry/data", `{"confirm":"registry.example.com"}`); rec.Code != 204 {
		t.Fatalf("delete data = %d %s", rec.Code, rec.Body)
	}
}

func TestCreateCredentialReturnsTokenOnce(t *testing.T) {
	f := newAPI(t)
	rec := f.do("POST", "/api/registry/credentials", `{"name":"github-ci"}`)
	var created struct {
		Credential struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"credential"`
		Token string `json:"token"`
	}
	if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &created) != nil || created.Token == "" || created.Credential.Name != "github-ci" {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	list := f.do("GET", "/api/registry/credentials", "").Body.String()
	if strings.Contains(list, created.Token) || strings.Contains(list, "$2a$") || !strings.Contains(list, "github-ci") {
		t.Fatalf("list = %s", list)
	}
	if rec := f.do("POST", "/api/registry/credentials", `{"name":"relist-panel"}`); rec.Code != 422 {
		t.Fatalf("reserved = %d", rec.Code)
	}
	if rec := f.do("POST", "/api/registry/credentials", `{"name":"github-ci"}`); rec.Code != 409 {
		t.Fatalf("duplicate = %d", rec.Code)
	}
	if rec := f.do("DELETE", "/api/registry/credentials/"+itoa(created.Credential.ID), ""); rec.Code != 204 {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
}

func TestRepositoriesList(t *testing.T) {
	f := newAPI(t)
	rec := f.do("GET", "/api/registry/repositories", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"name":"app"`) || !strings.Contains(rec.Body.String(), `"tagCount":2`) ||
		!strings.Contains(rec.Body.String(), `"name":"team/web"`) {
		t.Fatalf("repositories = %d %s", rec.Code, rec.Body)
	}
}

func TestTagsMarkInUseFromBothSources(t *testing.T) {
	f := newAPI(t)
	shop := User{ComposeID: 1, ComposeName: "shop", ProjectID: 7, ProjectName: "Acme", EnvironmentID: 3, EnvironmentName: "prod"}
	f.deployed["registry.example.com/app:1"] = []User{shop}
	f.running["registry.example.com/app:1"] = []User{{ComposeID: 1}}
	f.running["registry.example.com/app@"+plainDigest] = []User{{ComposeID: 2, ComposeName: "blog"}}
	rec := f.do("GET", "/api/registry/tags?repo=app", "")
	var tags []struct {
		Tag     string `json:"tag"`
		Digest  string `json:"digest"`
		Size    int64  `json:"size"`
		InUseBy []User `json:"inUseBy"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &tags) != nil || len(tags) != 2 {
		t.Fatalf("tags = %d %s", rec.Code, rec.Body)
	}
	byTag := map[string][]User{}
	for _, tg := range tags {
		byTag[tg.Tag] = tg.InUseBy
	}
	if len(byTag["1"]) != 1 || byTag["1"][0].ComposeName != "shop" {
		t.Fatalf("app:1 in use by %+v, want shop once", byTag["1"])
	}
	if len(byTag["2"]) != 1 || byTag["2"][0].ComposeName != "blog" {
		t.Fatalf("app:2 in use by %+v, want blog by digest", byTag["2"])
	}
}

func TestDeleteTagListsSharedDigestTags(t *testing.T) {
	f := newAPI(t)
	rec := f.do("DELETE", "/api/registry/tags?repo=app&tag=2", `{}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"deleted":["2"]`) {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	if len(*f.deleted) != 1 || (*f.deleted)[0] != plainDigest {
		t.Fatalf("deleted digests = %v", *f.deleted)
	}
}

func TestDeleteInUseTagNeedsConfirm(t *testing.T) {
	f := newAPI(t)
	f.deployed["registry.example.com/app:1"] = []User{{ComposeID: 1, ComposeName: "shop"}}
	if rec := f.do("DELETE", "/api/registry/tags?repo=app&tag=1", `{}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "shop") {
		t.Fatalf("unconfirmed = %d %s", rec.Code, rec.Body)
	}
	if len(*f.deleted) != 0 {
		t.Fatalf("deleted without confirm: %v", *f.deleted)
	}
	if rec := f.do("DELETE", "/api/registry/tags?repo=app&tag=1", `{"confirm":"1"}`); rec.Code != 200 {
		t.Fatalf("confirmed = %d %s", rec.Code, rec.Body)
	}
}

func TestRepositoriesWhenOff(t *testing.T) {
	f := newAPI(t)
	if _, err := f.svc.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec := f.do("GET", "/api/registry/repositories", ""); rec.Code != 409 || !strings.Contains(rec.Body.String(), "Registry is off") {
		t.Fatalf("off = %d %s", rec.Code, rec.Body)
	}
}

func TestCheckClassifiesFailures(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		code int
		want string
	}{
		{"dns", &net.DNSError{Err: "no such host", Name: "registry.example.com", IsNotFound: true}, 0, "dns"},
		{"tls", errors.New("tls: failed to verify certificate: x509: certificate signed by unknown authority"), 0, "tls"},
		{"refused", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}, 0, "connect"},
		{"401", nil, 401, "auth"},
		{"403", nil, 403, "auth"},
		{"404", nil, 404, "http"},
		{"200", nil, 200, ""},
	} {
		if got := checkStep(c.err, c.code); got != c.want {
			t.Errorf("%s: checkStep = %q, want %q", c.name, got, c.want)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func answer(code int, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: code, Header: header, Body: io.NopCloser(strings.NewReader(""))}
}

// checkWith runs Check endpoint against a host answering with respond, and
// returns the Authorization headers it saw.
func checkWith(t *testing.T, respond func(*http.Request) *http.Response) (map[string]any, []string) {
	t.Helper()
	f := newAPI(t)
	var auths []string
	f.mux = http.NewServeMux()
	client, _ := fakeRegistry(t)
	NewAPI(APIDeps{
		Service: f.svc, RegistryBase: client.Base, HTTP: client.HTTP,
		CheckHTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			auths = append(auths, r.Header.Get("Authorization"))
			return respond(r), nil
		})},
	}).RegisterRoutes(f.mux)
	rec := f.do("POST", "/api/registry/check", "")
	var out map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("check = %d %s", rec.Code, rec.Body)
	}
	return out, auths
}

func TestCheckSendsLoginOnlyToThisRegistry(t *testing.T) {
	out, auths := checkWith(t, func(r *http.Request) *http.Response {
		if r.Header.Get("Authorization") == "" {
			return answer(401, http.Header{"Www-Authenticate": {`Basic realm="relist-registry"`}})
		}
		return answer(200, nil)
	})
	if out["ok"] != true || len(auths) != 2 || auths[0] != "" || auths[1] == "" {
		t.Fatalf("relist realm: out = %v, auths = %q", out, auths)
	}

	out, auths = checkWith(t, func(*http.Request) *http.Response {
		return answer(401, http.Header{"Www-Authenticate": {`Basic realm="someone else"`}})
	})
	if out["ok"] != false || out["step"] != "auth" || len(auths) != 1 || auths[0] != "" {
		t.Fatalf("other realm: out = %v, auths = %q", out, auths)
	}

	out, auths = checkWith(t, func(*http.Request) *http.Response { return answer(404, nil) })
	if out["ok"] != false || out["step"] != "http" || len(auths) != 1 || auths[0] != "" {
		t.Fatalf("not a registry: out = %v, auths = %q", out, auths)
	}
}

func TestRegistryCallsRepairAMissingContainerFirst(t *testing.T) {
	f := newAPI(t)
	f.host.state = ContainerState{}
	f.host.calls = nil
	if rec := f.do("GET", "/api/registry/repositories", ""); rec.Code != 200 {
		t.Fatalf("repositories = %d %s", rec.Code, rec.Body)
	}
	if !slices.Contains(f.host.calls, "Create") {
		t.Fatalf("calls = %v, want the container recreated first", f.host.calls)
	}
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

func TestCleanupSettingsRoundTrip(t *testing.T) {
	f := newAPI(t)
	rec := f.do("GET", "/api/registry/cleanup", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"keep":10`) || !strings.Contains(rec.Body.String(), `"next":"`) ||
		!strings.Contains(rec.Body.String(), `"runs":[]`) {
		t.Fatalf("get = %d %s", rec.Code, rec.Body)
	}
	rec = f.do("PUT", "/api/registry/cleanup", `{"enabled":true,"keep":3,"days":["mon","fri"],"minute":90}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"days":["mon","fri"]`) {
		t.Fatalf("put = %d %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{
		`{"enabled":true,"keep":0,"days":["daily"],"minute":90}`,
		`{"enabled":true,"keep":3,"days":[],"minute":90}`,
		`{"enabled":true,"keep":3,"days":["funday"],"minute":90}`,
		`{"enabled":true,"keep":3,"days":["daily"],"minute":1440}`,
	} {
		if rec := f.do("PUT", "/api/registry/cleanup", bad); rec.Code != 422 {
			t.Errorf("%s = %d %s, want 422", bad, rec.Code, rec.Body)
		}
	}
}

func TestCleanupPreviewAndKept(t *testing.T) {
	f := newAPI(t)
	c, _ := f.store.CleanupSettings(context.Background())
	c.Keep = 1
	_ = f.store.SaveCleanupSettings(context.Background(), c)
	rec := f.do("GET", "/api/registry/cleanup/preview", "")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"app":["2"]}` {
		t.Fatalf("preview = %d %s", rec.Code, rec.Body)
	}
	rec = f.do("GET", "/api/registry/tags?repo=app", "")
	if !strings.Contains(rec.Body.String(), `"tag":"1"`) {
		t.Fatalf("tags = %s", rec.Body)
	}
	var tags []struct {
		Tag  string `json:"tag"`
		Kept bool   `json:"kept"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &tags)
	kept := map[string]bool{}
	for _, tg := range tags {
		kept[tg.Tag] = tg.Kept
	}
	if !kept["1"] || kept["2"] {
		t.Fatalf("kept = %v, want 1 only", kept)
	}
	// In use keeps it too, and the preview agrees.
	f.deployed["registry.example.com/app:2"] = []User{{ComposeID: 1, ComposeName: "shop"}}
	if rec := f.do("GET", "/api/registry/cleanup/preview", ""); strings.TrimSpace(rec.Body.String()) != `{}` {
		t.Fatalf("preview with app:2 in use = %s", rec.Body)
	}
}

func TestRunNowConflict(t *testing.T) {
	f := newAPI(t)
	f.gate.busy.Store(true)
	if rec := f.do("POST", "/api/registry/cleanup/run", ""); rec.Code != 202 || !strings.Contains(rec.Body.String(), `"runId":`) {
		t.Fatalf("first = %d %s", rec.Code, rec.Body)
	}
	rec := f.do("POST", "/api/registry/cleanup/run", "")
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "a Registry cleanup is already running") {
		t.Fatalf("second = %d %s", rec.Code, rec.Body)
	}
	f.gate.busy.Store(false)
	if err := f.cleaner.WaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// I3: switching automatic cleanup on starts the schedule from now.
func TestTurningCleanupOnArmsSchedule(t *testing.T) {
	f := newAPI(t)
	ctx := context.Background()
	old := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)
	c, _ := f.store.CleanupSettings(ctx)
	c.Enabled, c.LastRunAt = false, &old
	_ = f.store.SaveCleanupSettings(ctx, c)
	if rec := f.do("PUT", "/api/registry/cleanup", `{"enabled":true,"keep":3,"days":["daily"],"minute":180}`); rec.Code != 200 {
		t.Fatalf("put = %d %s", rec.Code, rec.Body)
	}
	if c, _ := f.store.CleanupSettings(ctx); c.LastRunAt == nil || !c.LastRunAt.After(old) {
		t.Fatalf("lastRunAt = %v, want now", c.LastRunAt)
	}
}

func TestRepoAndTagAreChecked(t *testing.T) {
	f := newAPI(t)
	for _, path := range []string{
		"/api/registry/tags?repo=",
		"/api/registry/tags?repo=App",
		"/api/registry/tags?repo=a/../b",
		"/api/registry/tags?repo=a%3Fb",
		"/api/registry/tags?repo=a//b",
	} {
		if rec := f.do("GET", path, ""); rec.Code != 400 {
			t.Errorf("GET %s = %d %s, want 400", path, rec.Code, rec.Body)
		}
	}
	for _, path := range []string{"/api/registry/tags?repo=app&tag=", "/api/registry/tags?repo=app&tag=bad%20tag", "/api/registry/tags?repo=app&tag=.x"} {
		if rec := f.do("DELETE", path, `{}`); rec.Code != 400 {
			t.Errorf("DELETE %s = %d %s, want 400", path, rec.Code, rec.Body)
		}
	}
	if rec := f.do("GET", "/api/registry/tags?repo=team/web", ""); rec.Code != 200 {
		t.Fatalf("a nested repo = %d %s", rec.Code, rec.Body)
	}
}

func TestRepositoriesShowNewestTag(t *testing.T) {
	f := newAPI(t)
	rec := f.do("GET", "/api/registry/repositories", "")
	var repos []struct {
		Name   string     `json:"name"`
		Newest *time.Time `json:"newest"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &repos) != nil {
		t.Fatalf("repositories = %d %s", rec.Code, rec.Body)
	}
	byName := map[string]*time.Time{}
	for _, r := range repos {
		byName[r.Name] = r.Newest
	}
	if n := byName["app"]; n == nil || !n.Equal(time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("app newest = %v", n)
	}
	if byName["team/web"] != nil {
		t.Fatalf("an empty repo has a newest tag: %v", byName["team/web"])
	}
}

// A tag's info is read once per manifest digest; later loads only HEAD it.
func TestTagInfoIsReadOncePerDigest(t *testing.T) {
	f := newAPI(t)
	f.do("GET", "/api/registry/tags?repo=app", "")
	first := f.requests.Load()
	f.requests.Store(0)
	if rec := f.do("GET", "/api/registry/tags?repo=app", ""); rec.Code != 200 {
		t.Fatalf("tags = %d %s", rec.Code, rec.Body)
	}
	// tags/list plus one HEAD per tag
	if again := f.requests.Load(); again != 3 || again >= first {
		t.Fatalf("second load sent %d requests (first %d), want 3", again, first)
	}
}

func TestDiskUsageIsReadOnceAMinute(t *testing.T) {
	f := newAPI(t)
	f.host.volumeReads = 0
	f.do("GET", "/api/registry", "")
	f.do("GET", "/api/registry", "")
	if f.host.volumeReads != 1 {
		t.Fatalf("volume read %d times, want 1", f.host.volumeReads)
	}
}

func TestUnknownCreatedIsNull(t *testing.T) {
	if r := toTagResponse(TagInfo{Tag: "x", Digest: "d"}, nil); r.Created != nil {
		t.Fatalf("created = %v, want null", r.Created)
	}
	at := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	if r := toTagResponse(TagInfo{Tag: "x", Digest: "d", Created: at}, nil); r.Created == nil || !r.Created.Equal(at) {
		t.Fatalf("created = %v", r.Created)
	}
}

// One tag that cannot be read must not blank the Repositories list.
func TestOneUnreadableTagKeepsRepositories(t *testing.T) {
	f := newAPI(t)
	fail := func(r *http.Request) bool { return r.URL.Path == "/v2/app/manifests/2" }
	f.fail.Store(&fail)
	rec := f.do("GET", "/api/registry/repositories", "")
	var repos []struct {
		Name     string     `json:"name"`
		TagCount int        `json:"tagCount"`
		Newest   *time.Time `json:"newest"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &repos) != nil {
		t.Fatalf("repositories = %d %s", rec.Code, rec.Body)
	}
	want := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	if repos[0].Name != "app" || repos[0].TagCount != 2 || repos[0].Newest == nil || !repos[0].Newest.Equal(want) {
		t.Fatalf("app = %+v", repos[0])
	}
}

// A created time that could not be read is read again on the next load.
func TestUnreadCreatedIsNotCached(t *testing.T) {
	f := newAPI(t)
	fail := func(r *http.Request) bool { return r.URL.Path == "/v2/app/blobs/"+plainConfig }
	f.fail.Store(&fail)
	if rec := f.do("GET", "/api/registry/tags?repo=app", ""); !strings.Contains(rec.Body.String(), `"created":null`) {
		t.Fatalf("first load = %d %s", rec.Code, rec.Body)
	}
	f.fail.Store(nil)
	if rec := f.do("GET", "/api/registry/tags?repo=app", ""); strings.Contains(rec.Body.String(), `"created":null`) {
		t.Fatalf("second load kept the unread time: %s", rec.Body)
	}
}

// Actions that change the volume's usage show it again on the next load.
func TestDiskUsageIsReadAgainAfterItChanges(t *testing.T) {
	f := newAPI(t)
	reads := func() int {
		f.host.volumeReads = 0
		f.do("GET", "/api/registry", "")
		return f.host.volumeReads
	}
	reads() // primes the cache
	if rec := f.do("POST", "/api/registry/cleanup/run", ""); rec.Code != 202 {
		t.Fatalf("run = %d %s", rec.Code, rec.Body)
	}
	if err := f.cleaner.WaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := reads(); n != 1 {
		t.Fatalf("after a cleanup run: volume read %d times, want 1", n)
	}
	if _, err := f.svc.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := reads(); n != 1 {
		t.Fatalf("after Disable: volume read %d times, want 1", n)
	}
	if rec := f.do("DELETE", "/api/registry/data", `{"confirm":"registry.example.com"}`); rec.Code != 204 {
		t.Fatalf("delete data = %d %s", rec.Code, rec.Body)
	}
	if n := reads(); n != 1 {
		t.Fatalf("after deleting data: volume read %d times, want 1", n)
	}
}
