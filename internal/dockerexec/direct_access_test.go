package dockerexec_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/directaccess"
	"relist-panel/internal/dockerexec"
)

type proxyDaemon struct {
	mu       sync.Mutex
	calls    []string
	bodies   map[string][]byte
	startErr string // non-empty: start answers 500 with this message
	ports    string // inspect NetworkSettings.Ports JSON
	state    string // inspect State JSON, default running
	list     string
}

func (d *proxyDaemon) handler(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := r.Method + " " + r.URL.Path
	d.calls = append(d.calls, key)
	raw, _ := io.ReadAll(r.Body)
	if d.bodies == nil {
		d.bodies = map[string][]byte{}
	}
	d.bodies[key] = raw
	d.bodies[r.Method+" "+r.URL.RequestURI()] = raw
	switch {
	case key == "POST /containers/create":
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id":"c1"}`))
	case strings.HasSuffix(key, "/start"):
		if d.startErr != "" {
			http.Error(w, `{"message":"`+d.startErr+`"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case key == "GET /containers/json":
		_, _ = w.Write([]byte(d.list))
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/json") && strings.HasPrefix(r.URL.Path, "/containers/"):
		st := d.state
		if st == "" {
			st = `{"Running":true}`
		}
		if st == "missing" {
			http.Error(w, `{"message":"No such container"}`, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"State":` + st + `,"NetworkSettings":{"Ports":` + d.ports + `}}`))
	case r.Method == http.MethodDelete:
		if strings.Contains(r.URL.Path, "relist-access-404") {
			http.Error(w, `{"message":"No such container"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func newProxyHost(t *testing.T) (*dockerexec.ProxyHost, *proxyDaemon) {
	t.Helper()
	d := &proxyDaemon{ports: `{"5432/tcp":[{"HostIp":"127.0.0.1","HostPort":"49153"}]}`}
	h := dockerexec.NewProxyHost(newFakeDaemon(t, http.HandlerFunc(d.handler)))
	h.SetImageResolver(func(context.Context) (string, error) { return "relist:test", nil })
	return h, d
}

func proxySpec() directaccess.ProxySpec {
	return directaccess.ProxySpec{
		AccessID: 9, TargetLabel: "database:7", Network: "relist-network", Upstream: "relist-db-7:5432",
		ListenPort: 5432, BindIP: "127.0.0.1", Deadline: time.Unix(1900000000, 0),
	}
}

func TestStartProxyRequest(t *testing.T) {
	h, d := newProxyHost(t)
	port, err := h.StartProxy(context.Background(), proxySpec())
	if err != nil || port != 49153 {
		t.Fatalf("StartProxy = %d, %v; want 49153 read back from inspect", port, err)
	}
	raw := d.bodies["POST /containers/create?name=relist-access-9"]
	var b struct {
		Image        string
		Entrypoint   []string
		Cmd          []string
		Labels       map[string]string
		ExposedPorts map[string]struct{}
		HostConfig   struct {
			AutoRemove     bool
			RestartPolicy  struct{ Name string }
			NetworkMode    string
			Memory         int64
			PidsLimit      int64
			CapDrop        []string
			SecurityOpt    []string
			ReadonlyRootfs bool
			Tmpfs          map[string]string
			PortBindings   map[string][]struct{ HostIp, HostPort string }
		}
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	if b.Image != "relist:test" || !slices.Equal(b.Entrypoint, []string{"/usr/local/bin/relist-proxy"}) {
		t.Fatalf("image/entrypoint = %q %v", b.Image, b.Entrypoint)
	}
	wantCmd := []string{"serve", "--listen", ":5432", "--target", "relist-db-7:5432", "--deadline", "1900000000", "--state", "/tmp/deadline"}
	if !slices.Equal(b.Cmd, wantCmd) {
		t.Fatalf("Cmd = %v", b.Cmd)
	}
	if _, ok := b.ExposedPorts["5432/tcp"]; !ok {
		t.Fatalf("ExposedPorts = %v", b.ExposedPorts)
	}
	pb := b.HostConfig.PortBindings["5432/tcp"]
	if len(pb) != 1 || pb[0].HostIp != "127.0.0.1" || pb[0].HostPort != "" {
		t.Fatalf("PortBindings = %+v", b.HostConfig.PortBindings)
	}
	hc := b.HostConfig
	if !hc.AutoRemove || hc.RestartPolicy.Name != "no" || hc.NetworkMode != "relist-network" ||
		hc.Memory != 33554432 || hc.PidsLimit != 64 || !slices.Equal(hc.CapDrop, []string{"ALL"}) ||
		!slices.Equal(hc.SecurityOpt, []string{"no-new-privileges"}) || !hc.ReadonlyRootfs || hc.Tmpfs["/tmp"] != "size=1m" {
		t.Fatalf("HostConfig = %+v", hc)
	}
	if b.Labels[directaccess.IDLabel] != "9" || b.Labels[directaccess.TargetLabel] != "database:7" || b.Labels[directaccess.ExpiresLabel] != "1900000000" {
		t.Fatalf("Labels = %v", b.Labels)
	}
	if !slices.Contains(d.calls, "POST /containers/relist-access-9/start") {
		t.Fatalf("calls = %v", d.calls)
	}
}

func TestStartProxyChosenPort(t *testing.T) {
	h, d := newProxyHost(t)
	spec := proxySpec()
	spec.HostPort, spec.BindIP = 15432, "0.0.0.0"
	if _, err := h.StartProxy(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	var b struct {
		HostConfig struct {
			PortBindings map[string][]struct{ HostIp, HostPort string }
		}
	}
	_ = json.Unmarshal(d.bodies["POST /containers/create?name=relist-access-9"], &b)
	pb := b.HostConfig.PortBindings["5432/tcp"]
	if len(pb) != 1 || pb[0].HostPort != "15432" || pb[0].HostIp != "0.0.0.0" {
		t.Fatalf("PortBindings = %+v", b.HostConfig.PortBindings)
	}
}

func TestStartProxyPortInUse(t *testing.T) {
	h, d := newProxyHost(t)
	d.startErr = "driver failed programming external connectivity: Bind for 127.0.0.1:15432 failed: port is already allocated"
	spec := proxySpec()
	spec.HostPort = 15432
	_, err := h.StartProxy(context.Background(), spec)
	if !errors.Is(err, directaccess.ErrPortInUse) || !strings.Contains(err.Error(), "15432") {
		t.Fatalf("err = %v, want ErrPortInUse naming 15432", err)
	}
	if !slices.Contains(d.calls, "DELETE /containers/relist-access-9") {
		t.Fatalf("the created container was not removed: %v", d.calls)
	}
}

func TestStartProxyOtherFailureRemoves(t *testing.T) {
	h, d := newProxyHost(t)
	d.startErr = "boom"
	_, err := h.StartProxy(context.Background(), proxySpec())
	if err == nil || errors.Is(err, directaccess.ErrPortInUse) {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(d.calls, "DELETE /containers/relist-access-9") {
		t.Fatalf("calls = %v", d.calls)
	}
}

func TestListProxies(t *testing.T) {
	h, d := newProxyHost(t)
	d.list = `[{"State":"running","Labels":{"relist.access.id":"4"}},{"State":"exited","Labels":{"relist.access.id":"5"}},{"State":"running","Labels":{"relist.access.id":"junk"}}]`
	got, err := h.ListProxies(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []directaccess.ProxyContainer{{AccessID: 4, Running: true}, {AccessID: 5, Running: false}}
	if !slices.Equal(got, want) {
		t.Fatalf("ListProxies = %+v", got)
	}
}

func TestRemoveProxyMissing(t *testing.T) {
	h, _ := newProxyHost(t)
	if err := h.RemoveProxy(context.Background(), 404); err != nil {
		t.Fatalf("RemoveProxy of a missing container = %v, want nil", err)
	}
}

func TestExtendGone(t *testing.T) {
	// Missing: the exec create answers 404.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /containers/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"No such container"}`, http.StatusNotFound)
	})
	h := dockerexec.NewProxyHost(newTerminalDaemon(t, mux))
	if err := h.Extend(context.Background(), 9, time.Unix(1900000000, 0)); !errors.Is(err, directaccess.ErrGone) {
		t.Fatalf("missing: %v, want ErrGone", err)
	}
	// Stopped: Docker answers 409.
	mux = http.NewServeMux()
	mux.HandleFunc("POST /containers/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Container abc is not running"}`, http.StatusConflict)
	})
	h = dockerexec.NewProxyHost(newTerminalDaemon(t, mux))
	if err := h.Extend(context.Background(), 9, time.Unix(1900000000, 0)); !errors.Is(err, directaccess.ErrGone) {
		t.Fatalf("stopped: %v, want ErrGone", err)
	}
}

func TestExtendRunsTheProxyCommand(t *testing.T) {
	d := &execDaemon{code: 0}
	d.reply = func([]byte) []byte { return nil }
	h := dockerexec.NewProxyHost(newTerminalDaemon(t, d.handler(t)))
	if err := h.Extend(context.Background(), 9, time.Unix(1900000000, 0)); err != nil {
		t.Fatal(err)
	}
	var body struct{ Cmd []string }
	_ = json.Unmarshal(d.create, &body)
	want := []string{"/usr/local/bin/relist-proxy", "extend", "--state", "/tmp/deadline", "1900000000"}
	if !slices.Equal(body.Cmd, want) || d.path != "/containers/relist-access-9/exec" {
		t.Fatalf("exec %s %v", d.path, body.Cmd)
	}
}
