package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

type apiFixture struct {
	*svcFixture
	mux      *http.ServeMux
	deleted  *[]string
	deployed map[string][]User
	running  map[string][]User
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
	f.mux = http.NewServeMux()
	NewAPI(APIDeps{
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
		err  string
		code int
		want string
	}{
		{"dial tcp: lookup registry.example.com: no such host", 0, "dns"},
		{"tls: failed to verify certificate: x509: certificate signed by unknown authority", 0, "tls"},
		{"", 401, "auth"},
		{"", 200, ""},
	} {
		if got := checkStep(c.err, c.code); got != c.want {
			t.Errorf("checkStep(%q, %d) = %q, want %q", c.err, c.code, got, c.want)
		}
	}
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }
