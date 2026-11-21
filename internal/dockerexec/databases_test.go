package dockerexec_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"relist-panel/internal/databases"
	"relist-panel/internal/dockerexec"
)

// fakeDBDaemon records calls and bodies, and answers from canned maps.
type fakeDBDaemon struct {
	mu      sync.Mutex
	calls   []string
	bodies  map[string]json.RawMessage // "METHOD path" -> body
	inspect string                     // canned container inspect JSON ("" = 404)
	volume  bool                       // GET /volumes/<name> exists
}

func (f *fakeDBDaemon) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := r.Method + " " + r.URL.Path
	f.calls = append(f.calls, key)
	raw, _ := io.ReadAll(r.Body)
	if len(raw) > 0 {
		if f.bodies == nil {
			f.bodies = map[string]json.RawMessage{}
		}
		f.bodies[key] = raw
		f.bodies[r.Method+" "+r.URL.RequestURI()] = raw
	}
	switch {
	case key == "POST /containers/create":
		_, _ = w.Write([]byte(`{"Id":"new"}`))
	case key == "POST /volumes/create":
		_, _ = w.Write([]byte(`{}`))
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/volumes/"):
		if f.volume {
			_, _ = w.Write([]byte(`{"Name":"x"}`))
			return
		}
		http.Error(w, `{"message":"no such volume"}`, http.StatusNotFound)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json"):
		if f.inspect == "" {
			http.Error(w, `{"message":"No such container"}`, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(f.inspect))
	case r.Method == http.MethodDelete:
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
	case strings.HasPrefix(r.URL.Path, "/images/") && strings.HasSuffix(r.URL.Path, "/json"):
		_, _ = w.Write([]byte(`{"Id":"sha256:abc"}`))
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func newDBHost(t *testing.T) (*dockerexec.DatabaseHost, *fakeDBDaemon) {
	t.Helper()
	f := &fakeDBDaemon{}
	return dockerexec.NewDatabaseHost(newFakeDaemon(t, http.HandlerFunc(f.handler))), f
}

func dbSpec() databases.ContainerSpec {
	return databases.ContainerSpec{
		Name: "relist-db-1", Volume: "relist-db-1", Image: "postgres:18", DataPath: "/var/lib/postgresql",
		Env: []string{"POSTGRES_PASSWORD=pw"}, Healthcheck: []string{"CMD-SHELL", "pg_isready"},
		Alias: "shop.db.internal", MemoryBytes: 512 << 20, NanoCPUs: 1_500_000_000,
		Labels: map[string]string{databases.IDLabel: "1", databases.HashLabel: "h"},
	}
}

func TestDatabaseHostCreateBody(t *testing.T) {
	h, f := newDBHost(t)
	if err := h.Create(context.Background(), dbSpec()); err != nil {
		t.Fatal(err)
	}
	raw := f.bodies["POST /containers/create?name=relist-db-1"]
	if strings.Contains(string(raw), "PortBindings") || strings.Contains(string(raw), "PublishAllPorts") {
		t.Fatalf("publishes a port: %s", raw)
	}
	var body struct {
		Image       string
		Env         []string
		Labels      map[string]string
		Healthcheck struct {
			Test                                    []string
			Interval, Timeout, StartPeriod, Retries int64
		}
		HostConfig struct {
			Binds         []string
			NetworkMode   string
			RestartPolicy struct{ Name string }
			Memory        int64
			NanoCpus      int64
			LogConfig     struct {
				Type   string
				Config map[string]string
			}
		}
		NetworkingConfig struct {
			EndpointsConfig map[string]struct{ Aliases []string }
		}
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	hc := body.HostConfig
	if !slices.Equal(hc.Binds, []string{"relist-db-1:/var/lib/postgresql"}) || hc.NetworkMode != "relist-network" ||
		hc.RestartPolicy.Name != "unless-stopped" || hc.Memory != 512<<20 || hc.NanoCpus != 1_500_000_000 ||
		hc.LogConfig.Type != "json-file" || hc.LogConfig.Config["max-size"] != "10m" || hc.LogConfig.Config["max-file"] != "5" {
		t.Fatalf("HostConfig = %+v", hc)
	}
	if got := body.NetworkingConfig.EndpointsConfig["relist-network"].Aliases; !slices.Equal(got, []string{"shop.db.internal"}) {
		t.Fatalf("aliases = %v", got)
	}
	if !slices.Equal(body.Healthcheck.Test, []string{"CMD-SHELL", "pg_isready"}) || body.Healthcheck.Retries != 5 {
		t.Fatalf("healthcheck = %+v", body.Healthcheck)
	}
	if body.Labels["relist.database.hash"] != "h" || body.Image != "postgres:18" {
		t.Fatalf("labels/image = %v %s", body.Labels, body.Image)
	}
	if !slices.Contains(f.calls, "POST /containers/relist-db-1/start") {
		t.Fatalf("not started: %v", f.calls)
	}
}

func TestDatabaseHostCreateMakesVolume(t *testing.T) {
	h, f := newDBHost(t)
	if err := h.Create(context.Background(), dbSpec()); err != nil {
		t.Fatal(err)
	}
	var vol struct {
		Name   string
		Labels map[string]string
	}
	if err := json.Unmarshal(f.bodies["POST /volumes/create"], &vol); err != nil {
		t.Fatal(err)
	}
	if vol.Name != "relist-db-1" || vol.Labels["relist.database.id"] != "1" {
		t.Fatalf("volume = %+v", vol)
	}
	// An existing volume is left alone.
	h2, f2 := newDBHost(t)
	f2.volume = true
	if err := h2.Create(context.Background(), dbSpec()); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(f2.calls, "POST /volumes/create") {
		t.Fatal("recreated an existing volume")
	}
}

func TestDatabaseHostInspect(t *testing.T) {
	h, f := newDBHost(t)
	f.inspect = `{"State":{"Running":true,"Health":{"Status":"healthy"}},"Image":"sha256:img",
		"Config":{"Labels":{"relist.database.hash":"abc"}},
		"NetworkSettings":{"Networks":{"relist-network":{"Aliases":["shop.db.internal","relist-db-1"]},"other":{"Aliases":["x"]}}}}`
	st, err := h.Inspect(context.Background(), "relist-db-1")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Exists || !st.Running || st.Health != "healthy" || st.Hash != "abc" || st.ImageID != "sha256:img" ||
		!slices.Equal(st.Aliases, []string{"shop.db.internal", "relist-db-1"}) {
		t.Fatalf("state = %+v", st)
	}
}

func TestDatabaseHostRealias(t *testing.T) {
	h, f := newDBHost(t)
	if err := h.Realias(context.Background(), "relist-db-1", "blog.db.internal"); err != nil {
		t.Fatal(err)
	}
	want := []string{"POST /networks/relist-network/disconnect", "POST /networks/relist-network/connect"}
	if !slices.Equal(f.calls, want) {
		t.Fatalf("calls = %v", f.calls)
	}
	var connect struct {
		Container      string
		EndpointConfig struct{ Aliases []string }
	}
	if err := json.Unmarshal(f.bodies["POST /networks/relist-network/connect"], &connect); err != nil {
		t.Fatal(err)
	}
	if connect.Container != "relist-db-1" || !slices.Equal(connect.EndpointConfig.Aliases, []string{"blog.db.internal"}) {
		t.Fatalf("connect = %+v", connect)
	}
}

func TestDatabaseHostNotFoundIsNil(t *testing.T) {
	h, _ := newDBHost(t)
	ctx := context.Background()
	if err := h.Remove(ctx, "relist-db-1"); err != nil {
		t.Errorf("Remove: %v", err)
	}
	if err := h.RemoveVolume(ctx, "relist-db-1"); err != nil {
		t.Errorf("RemoveVolume: %v", err)
	}
	st, err := h.Inspect(ctx, "relist-db-1")
	if err != nil || st.Exists {
		t.Errorf("Inspect = %+v, %v", st, err)
	}
}

// A recreate must let the database flush: stop with a grace period before the
// forced remove, never SIGKILL a running server.
func TestDatabaseHostCreateStopsBeforeRemove(t *testing.T) {
	h, f := newDBHost(t)
	f.inspect = `{"State":{"Running":true}}`
	if err := h.Create(context.Background(), dbSpec()); err != nil {
		t.Fatal(err)
	}
	stop := slices.Index(f.calls, "POST /containers/relist-db-1/stop")
	remove := slices.Index(f.calls, "DELETE /containers/relist-db-1")
	if stop < 0 || remove < 0 || stop > remove {
		t.Fatalf("want stop before remove, calls = %v", f.calls)
	}
}
