package dockerexec_test

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"relist-panel/internal/composes"
	"relist-panel/internal/dockerexec"
)

// requests records every call the engine made, in order.
type requests struct {
	mu   sync.Mutex
	seen []string
}

func (r *requests) add(req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, req.Method+" "+req.URL.Path+"?"+req.URL.RawQuery)
}

func (r *requests) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
}

func TestEngineProjectContainers(t *testing.T) {
	var filters string
	socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/containers/json" || req.URL.Query().Get("all") != "1" {
			http.Error(w, "unexpected "+req.URL.String(), http.StatusBadRequest)
			return
		}
		filters = req.URL.Query().Get("filters")
		_, _ = w.Write([]byte(`[
			{"Id":"a","Names":["/p-web-1"],"State":"running","Labels":{"com.docker.compose.service":"web","com.docker.compose.project":"p"}},
			{"Id":"b","Names":["/p-old-1"],"State":"exited","Labels":{"com.docker.compose.service":"old","com.docker.compose.project":"p"}}
		]`))
	}))
	got, err := dockerexec.NewEngine(socket).ProjectContainers(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	want := []composes.EngineContainer{
		{ID: "a", Name: "p-web-1", Service: "web", Project: "p", Running: true},
		{ID: "b", Name: "p-old-1", Service: "old", Project: "p", Running: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("containers = %+v, want %+v", got, want)
	}
	if want := `{"label":["com.docker.compose.project=p","com.docker.compose.oneoff=False"]}`; filters != want {
		t.Fatalf("filters = %s, want %s", filters, want)
	}
}

func composeContainersDaemon(t *testing.T, filters *string, query *url.Values) string {
	return newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/containers/json" || req.Method != http.MethodGet {
			http.Error(w, "unexpected "+req.URL.String(), http.StatusBadRequest)
			return
		}
		*query = req.URL.Query()
		*filters = req.URL.Query().Get("filters")
		_, _ = w.Write([]byte(`[
			{"Id":"a","Names":["/relist-c1-web-1"],"State":"running","Labels":{"com.docker.compose.service":"web","com.docker.compose.project":"relist-c1"}},
			{"Id":"b","Names":["/relist-c19-web-1"],"State":"exited","Labels":{"com.docker.compose.service":"web","com.docker.compose.project":"relist-c19"}},
			{"Id":"c","Names":["/relist-c19-db-1"],"State":"running","Labels":{"com.docker.compose.service":"db","com.docker.compose.project":"relist-c19"}}
		]`))
	}))
}

func TestComposeContainers(t *testing.T) {
	var filters string
	var query url.Values
	socket := composeContainersDaemon(t, &filters, &query)
	got, err := dockerexec.NewEngine(socket).ComposeContainers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []composes.EngineContainer{
		{ID: "a", Name: "relist-c1-web-1", Service: "web", Project: "relist-c1", Running: true},
		{ID: "b", Name: "relist-c19-web-1", Service: "web", Project: "relist-c19", Running: false},
		{ID: "c", Name: "relist-c19-db-1", Service: "db", Project: "relist-c19", Running: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("containers = %+v, want %+v", got, want)
	}
	if query.Get("all") != "1" {
		t.Fatalf("all = %q, want 1", query.Get("all"))
	}
	if want := `{"label":["com.docker.compose.project","com.docker.compose.oneoff=False"]}`; filters != want {
		t.Fatalf("filters = %s, want %s", filters, want)
	}
}

func TestComposeContainersSkipsOneOff(t *testing.T) {
	var filters string
	var query url.Values
	socket := composeContainersDaemon(t, &filters, &query)
	if _, err := dockerexec.NewEngine(socket).ComposeContainers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filters, "com.docker.compose.oneoff=False") {
		t.Fatalf("filters = %s, want the oneoff=False label", filters)
	}
}

func TestEngineInspect(t *testing.T) {
	socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/containers/n/json":
			_, _ = w.Write([]byte(`{
				"State":{"Running":true,"Health":{"Status":"starting","Log":[{"Output":"first"},{"Output":"connection refused"}]}},
				"Config":{"Labels":{"traefik.enable":"true","com.docker.compose.service":"web"}},
				"Mounts":[
					{"Type":"volume","Name":"abc123","Destination":"/var/lib/app"},
					{"Type":"volume","Name":"p_data","Destination":"/data"},
					{"Type":"bind","Source":"/srv","Destination":"/srv"}
				]}`))
		case "/volumes/abc123":
			_, _ = w.Write([]byte(`{"Name":"abc123","Labels":{"com.docker.volume.anonymous":""}}`))
		case "/volumes/p_data":
			_, _ = w.Write([]byte(`{"Name":"p_data","Labels":{"com.docker.compose.volume":"data"}}`))
		default:
			http.Error(w, "unexpected "+req.URL.Path, http.StatusNotFound)
		}
	}))
	got, err := dockerexec.NewEngine(socket).Inspect(context.Background(), "n")
	if err != nil {
		t.Fatal(err)
	}
	want := composes.EngineState{
		Running:          true,
		Health:           "starting",
		LastHealthOutput: "connection refused",
		Labels:           map[string]string{"traefik.enable": "true", "com.docker.compose.service": "web"},
		Mounts: []composes.EngineMount{
			{Destination: "/var/lib/app", Anonymous: true},
			{Destination: "/data", Anonymous: false},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("state = %+v, want %+v", got, want)
	}
}

func TestEngineStopAndRemove(t *testing.T) {
	rec := &requests{}
	socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rec.add(req)
		if req.URL.Path == "/containers/gone" {
			http.Error(w, `{"message":"No such container: gone"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	e := dockerexec.NewEngine(socket)
	ctx := context.Background()
	if err := e.Stop(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	if err := e.Remove(ctx, "x", true, true); err != nil {
		t.Fatal(err)
	}
	if err := e.Remove(ctx, "y", false, false); err != nil {
		t.Fatal(err)
	}
	if err := e.Remove(ctx, "gone", false, false); err != nil {
		t.Fatalf("remove of a missing container: %v", err)
	}
	got := rec.all()
	want := []string{
		"POST /containers/x/stop?",
		"DELETE /containers/x?force=1&v=1",
		"DELETE /containers/y?force=0&v=0",
		"DELETE /containers/gone?force=0&v=0",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}
}
