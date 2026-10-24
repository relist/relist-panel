package httpapi_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestVolumeAcknowledgementsBeforeMount(t *testing.T) {
	for _, tc := range []struct {
		name, source, user, service, root, bind string
		want                                    int
	}{
		{name: "explicit root", source: "demoapp_pgdata", user: "0:999", want: 400},
		{name: "zero padded root", source: "demoapp_pgdata", user: "000:999", want: 400},
		{name: "fallback root", source: "demoapp_pgdata", service: "web", want: 400},
		{name: "acknowledged root", source: "demoapp_pgdata", service: "web", root: "1", want: 200},
		{name: "external bind", source: "/srv/media", service: "db", want: 400},
		{name: "acknowledged bind", source: "/srv/media", service: "db", bind: "1", want: 200},
		{name: "non mounting service", source: "/srv/media", service: "web", bind: "1", root: "1", want: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newTestServer(t)
			setupAndLogin(t, server)
			fixture := setupExplorerCompose(t, server)
			server.volumeRunner.Stdout = `{"entries":[],"truncated":false}`
			response := server.request(t, http.MethodGet, volumeOpPath(fixture, "list", url.Values{"source": {tc.source}, "user": {tc.user}, "service": {tc.service}, "rootConfirmed": {tc.root}, "bindConfirmed": {tc.bind}}), nil)
			defer response.Body.Close()
			if response.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", response.StatusCode, tc.want)
			}
			if tc.want != http.StatusOK && len(server.volumeRunner.Calls) != 0 {
				t.Fatal("refused request mounted volume")
			}
		})
	}
}

func TestVolumeAcknowledgementsApplyToEveryOperation(t *testing.T) {
	for _, op := range []struct{ route, method string }{
		{"list", "GET"}, {"stat", "GET"}, {"read", "GET"}, {"archive", "GET"}, {"content", "PUT"}, {"content", "DELETE"}, {"restore", "PUT"},
	} {
		for _, risk := range []string{"root", "bind", "omitted root"} {
			t.Run(op.method+op.route+"/"+risk, func(t *testing.T) {
				server := newTestServer(t)
				setupAndLogin(t, server)
				f := setupExplorerCompose(t, server)
				source, service := "demoapp_pgdata", "web"
				if risk == "bind" {
					source, service = "/srv/media", "db"
				}
				if risk == "omitted root" {
					server.volumeCLI.ComposeConfigOutput = []byte(`{"services":{"web":{"volumes":[{"type":"volume","source":"pgdata","target":"/data"}]}},"volumes":{"pgdata":{"name":"demoapp_pgdata"}}}`)
					service = ""
				}
				q := url.Values{"source": {source}, "service": {service}, "path": {"config"}}
				request, _ := http.NewRequest(op.method, composeVolumesPath(f.project.ID, f.production.ID, f.compose.ID)+"/"+op.route+"?"+q.Encode(), strings.NewReader("bytes"))
				response := server.serve(request)
				defer response.Body.Close()
				if response.StatusCode != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400", response.StatusCode)
				}
				if len(server.volumeRunner.Calls) != 0 {
					t.Fatal("unacknowledged operation mounted volume")
				}
			})
		}
	}
}

func TestVolumeOperationsRequireExplicitIdentityEvenWithAcknowledgements(t *testing.T) {
	for _, op := range []struct{ route, method string }{
		{"list", "GET"}, {"stat", "GET"}, {"read", "GET"}, {"archive", "GET"}, {"content", "PUT"}, {"content", "DELETE"}, {"restore", "PUT"},
	} {
		for _, root := range []bool{false, true} {
			t.Run(op.method+op.route+"/root="+map[bool]string{false: "false", true: "true"}[root], func(t *testing.T) {
				server := newTestServer(t)
				setupAndLogin(t, server)
				f := setupExplorerCompose(t, server)
				if root {
					server.volumeCLI.ComposeConfigOutput = []byte(`{"services":{"web":{"volumes":[{"type":"volume","source":"pgdata","target":"/data"}]}},"volumes":{"pgdata":{"name":"demoapp_pgdata"}}}`)
				}
				q := url.Values{"source": {"demoapp_pgdata"}, "path": {"config"}, "rootConfirmed": {"1"}, "bindConfirmed": {"1"}}
				request, _ := http.NewRequest(op.method, composeVolumesPath(f.project.ID, f.production.ID, f.compose.ID)+"/"+op.route+"?"+q.Encode(), strings.NewReader("bytes"))
				response := server.serve(request)
				defer response.Body.Close()
				if response.StatusCode != http.StatusBadRequest {
					t.Fatalf("status=%d, want 400", response.StatusCode)
				}
				assertErrorContains(t, response, "select a mounting service or explicit user")
				if len(server.volumeRunner.Calls) != 0 {
					t.Fatal("omitted identity reached runner")
				}
			})
		}
	}
}
