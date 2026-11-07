package policy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture is a Compose directory and the rules over it.
type fixture struct {
	dir   string
	rules Rules
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".composes", "1")
	for _, d := range []string{"data", "src", ".ssh", "sets"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	srv := filepath.Join(root, "srv")
	if err := os.MkdirAll(srv, 0o755); err != nil {
		t.Fatal(err)
	}
	return fixture{dir: dir, rules: Rules{ComposeDir: dir, Roots: []string{srv}, ProjectName: "relist-c1", PanelDomain: "panel.example.com",
		Traefik: map[string]map[string]string{"web": {
			"traefik.enable":                                        "true",
			"traefik.http.routers.c1-web.rule":                      "Host(`app.example.com`)",
			"traefik.http.services.c1-web.loadbalancer.server.port": "80",
		}}}}
}

// config is a resolved config with one service "web" (plus any extra
// top-level keys).
func config(t *testing.T, web map[string]any, top map[string]any) []byte {
	t.Helper()
	doc := map[string]any{"name": "relist-c1", "services": map[string]any{"web": web}}
	for k, v := range top {
		doc[k] = v
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func bind(source string) map[string]any {
	return map[string]any{"type": "bind", "source": source, "target": "/mnt", "bind": map[string]any{}}
}

// ordinary is a Compose as the panel deploys it: named volume, a bind
// under the Compose directory, Domain labels, the shared network, the log
// cap, a Variable Set file, a healthcheck and resources.
func ordinary(f fixture) map[string]any {
	return map[string]any{
		"image":       "nginx:1.27",
		"command":     nil,
		"entrypoint":  nil,
		"environment": map[string]any{"A": "1"},
		"env_file":    []any{map[string]any{"path": filepath.Join(f.dir, "sets", "3.env")}},
		"labels": map[string]any{
			"traefik.enable":                                        "true",
			"traefik.http.routers.c1-web.rule":                      "Host(`app.example.com`)",
			"traefik.http.services.c1-web.loadbalancer.server.port": "80",
		},
		"logging":     map[string]any{"driver": "json-file", "options": map[string]any{"max-size": "10m", "max-file": "5"}},
		"networks":    map[string]any{"default": nil, "relist-network": map[string]any{"aliases": []any{"web.c1.internal"}}},
		"healthcheck": map[string]any{"test": []any{"CMD", "true"}},
		"deploy":      map[string]any{"resources": map[string]any{"limits": map[string]any{"memory": "512M"}}},
		"restart":     "unless-stopped",
		"ports":       []any{map[string]any{"target": 80, "published": "8080", "protocol": "tcp", "mode": "ingress"}},
		"depends_on":  map[string]any{},
		"volumes": []any{
			map[string]any{"type": "volume", "source": "db", "target": "/var/lib/db", "volume": map[string]any{}},
			bind(filepath.Join(f.dir, "data")),
			bind(filepath.Join(f.dir, "src", "conf")),
			map[string]any{"type": "tmpfs", "target": "/tmp"},
		},
	}
}

func ordinaryTop() map[string]any {
	return map[string]any{
		"volumes":  map[string]any{"db": map[string]any{"name": "relist-c1_db"}},
		"networks": map[string]any{"default": map[string]any{"name": "relist-c1_default"}, "relist-network": map[string]any{"name": "relist-network", "external": true}},
	}
}

func TestOrdinaryComposePasses(t *testing.T) {
	f := newFixture(t)
	violations, err := Check(config(t, ordinary(f), ordinaryTop()), "services: {web: {image: nginx}}", f.rules)
	if err != nil || len(violations) != 0 {
		t.Fatalf("violations = %+v, err = %v", violations, err)
	}
}

func TestEachViolation(t *testing.T) {
	f := newFixture(t)
	outside := t.TempDir()
	escape := filepath.Join(f.dir, "src", "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		web  map[string]any
		top  map[string]any
	}{
		{"bind outside", map[string]any{"volumes": []any{bind("/etc")}}, nil},
		{"docker socket", map[string]any{"volumes": []any{bind("/var/run/docker.sock")}}, nil},
		{"symlink escaping the source", map[string]any{"volumes": []any{bind(filepath.Join(escape, "x"))}}, nil},
		{"compose directory itself", map[string]any{"volumes": []any{bind(f.dir)}}, nil},
		{"ssh keys", map[string]any{"volumes": []any{bind(filepath.Join(f.dir, ".ssh"))}}, nil},
		{"nested binds", map[string]any{"volumes": []any{bind(filepath.Join(f.dir, "data")), map[string]any{"type": "bind", "source": filepath.Join(f.dir, "data", "inner"), "target": "/i"}}}, nil},
		{"env_file outside", map[string]any{"env_file": []any{map[string]any{"path": "/etc/environment"}}}, nil},
		{"label_file outside", map[string]any{"label_file": []any{"/etc/hostname"}}, nil},
		{"config file outside", map[string]any{"configs": []any{map[string]any{"source": "c"}}}, map[string]any{"configs": map[string]any{"c": map[string]any{"name": "relist-c1_c", "file": "/etc/passwd"}}}},
		{"secret file outside", map[string]any{"secrets": []any{map[string]any{"source": "s"}}}, map[string]any{"secrets": map[string]any{"s": map[string]any{"name": "relist-c1_s", "file": "/etc/shadow"}}}},
		{"secret from environment", map[string]any{"secrets": []any{map[string]any{"source": "s"}}}, map[string]any{"secrets": map[string]any{"s": map[string]any{"name": "relist-c1_s", "environment": "HOME"}}}},
		{"config from environment", map[string]any{"configs": []any{map[string]any{"source": "c"}}}, map[string]any{"configs": map[string]any{"c": map[string]any{"name": "relist-c1_c", "environment": "HOME"}}}},
		{"volume driver_opts", map[string]any{"volumes": []any{map[string]any{"type": "volume", "source": "v", "target": "/v"}}}, map[string]any{"volumes": map[string]any{"v": map[string]any{"name": "relist-c1_v", "driver_opts": map[string]any{"device": "/", "o": "bind", "type": "none"}}}}},
		{"volume driver", map[string]any{"volumes": []any{map[string]any{"type": "volume", "source": "v", "target": "/v"}}}, map[string]any{"volumes": map[string]any{"v": map[string]any{"name": "relist-c1_v", "driver": "nfs"}}}},
		{"volume external", map[string]any{"volumes": []any{map[string]any{"type": "volume", "source": "v", "target": "/v"}}}, map[string]any{"volumes": map[string]any{"v": map[string]any{"name": "other_db", "external": true}}}},
		{"volume foreign name", map[string]any{"volumes": []any{map[string]any{"type": "volume", "source": "v", "target": "/v"}}}, map[string]any{"volumes": map[string]any{"v": map[string]any{"name": "relist-c2_db"}}}},
		{"volumes_from container", map[string]any{"volumes_from": []any{"container:db"}}, nil},
		{"privileged", map[string]any{"privileged": true}, nil},
		{"cap_add", map[string]any{"cap_add": []any{"SYS_ADMIN"}}, nil},
		{"devices", map[string]any{"devices": []any{map[string]any{"source": "/dev/sda", "target": "/dev/sda"}}}, nil},
		{"device_cgroup_rules", map[string]any{"device_cgroup_rules": []any{"a *:* rwm"}}, nil},
		{"security_opt", map[string]any{"security_opt": []any{"seccomp=unconfined"}}, nil},
		{"sysctls", map[string]any{"sysctls": map[string]any{"kernel.x": "1"}}, nil},
		{"cgroup_parent", map[string]any{"cgroup_parent": "/"}, nil},
		{"runtime", map[string]any{"runtime": "runc"}, nil},
		{"use_api_socket", map[string]any{"use_api_socket": true}, nil},
		{"gpus", map[string]any{"gpus": []any{map[string]any{"count": -1}}}, nil},
		{"provider", map[string]any{"provider": map[string]any{"type": "model"}}, nil},
		{"userns_mode host", map[string]any{"userns_mode": "host"}, nil},
		{"pid host", map[string]any{"pid": "host"}, nil},
		{"ipc host", map[string]any{"ipc": "host"}, nil},
		{"uts host", map[string]any{"uts": "host"}, nil},
		{"cgroup host", map[string]any{"cgroup": "host"}, nil},
		{"network_mode host", map[string]any{"network_mode": "host"}, nil},
		{"network_mode container", map[string]any{"network_mode": "container:other"}, nil},
		{"pid of a service elsewhere", map[string]any{"pid": "service:nope"}, nil},
		{"hook privileged", map[string]any{"post_start": []any{map[string]any{"command": []any{"id"}, "privileged": true}}}, nil},
		{"image mount", map[string]any{"volumes": []any{map[string]any{"type": "image", "source": "alpine", "target": "/i"}}}, nil},
		{"npipe mount", map[string]any{"volumes": []any{map[string]any{"type": "npipe", "source": "x", "target": "/p"}}}, nil},
		{"build context outside", map[string]any{"build": map[string]any{"context": "/etc", "dockerfile": "Dockerfile"}}, nil},
		{"build context in a bind root", map[string]any{"build": map[string]any{"context": f.rules.Roots[0], "dockerfile": "Dockerfile"}}, nil},
		{"dockerfile outside", map[string]any{"build": map[string]any{"context": filepath.Join(f.dir, "src"), "dockerfile": "/etc/Dockerfile"}}, nil},
		{"additional context outside", map[string]any{"build": map[string]any{"context": filepath.Join(f.dir, "src"), "additional_contexts": map[string]any{"x": "/etc"}}}, nil},
		{"build network host", map[string]any{"build": map[string]any{"context": filepath.Join(f.dir, "src"), "network": "host"}}, nil},
		{"build privileged", map[string]any{"build": map[string]any{"context": filepath.Join(f.dir, "src"), "privileged": true}}, nil},
		{"build entitlements", map[string]any{"build": map[string]any{"context": filepath.Join(f.dir, "src"), "entitlements": []any{"network.host"}}}, nil},
		{"build ssh", map[string]any{"build": map[string]any{"context": filepath.Join(f.dir, "src"), "ssh": []any{"default"}}}, nil},
		{"unknown key", map[string]any{"made_up_key": true}, nil},
		{"traefik label naming the panel", map[string]any{"labels": map[string]any{"traefik.http.routers.x.rule": "Host(`panel.example.com`)"}}, nil},
		{"router for every path", map[string]any{"labels": map[string]any{"traefik.http.routers.x.rule": "PathPrefix(`/`)"}}, nil},
		{"router by regexp", map[string]any{"labels": map[string]any{"traefik.http.routers.x.rule": "HostRegexp(`.+`)"}}, nil},
		{"router for a host no Domain of this Compose has", map[string]any{"labels": map[string]any{"traefik.http.routers.x.rule": "Host(`victim.example.com`)"}}, nil},
		{"router widening a Domain", map[string]any{"labels": map[string]any{"traefik.http.routers.x.rule": "Host(`shop.example.com`)"}}, nil},
		{"panel router with another rule", map[string]any{"labels": map[string]any{"traefik.http.routers.c1-web.rule": "HostRegexp(`.+`)"}}, nil},
		{"service merged into the panel's", map[string]any{"labels": map[string]any{"traefik.enable": "true", "traefik.http.services.panel.loadbalancer.server.port": "80"}}, nil},
		{"middleware", map[string]any{"labels": map[string]any{"traefik.http.middlewares.x.headers.customrequestheaders.a": "b"}}, nil},
		{"tls options", map[string]any{"labels": map[string]any{"traefik.http.routers.c1-web.tls.options": "weak@file"}}, nil},
		{"shared network label elsewhere", map[string]any{"labels": map[string]any{"traefik.docker.network": "relist-c2_default"}}, nil},
		{"build traefik label", map[string]any{"build": map[string]any{"context": filepath.Join(f.dir, "src"), "labels": map[string]any{"traefik.enable": "true"}}}, nil},
		{"build docker label", map[string]any{"build": map[string]any{"context": filepath.Join(f.dir, "src"), "labels": map[string]any{"com.docker.compose.project": "relist-c2"}}}, nil},
		{"router key in another case", map[string]any{"labels": map[string]any{"Traefik.HTTP.Routers.x.Rule": "PathPrefix(`/`)"}}, nil},
		{"router priority", map[string]any{"labels": map[string]any{"traefik.http.routers.c1-web.priority": "999999999"}}, nil},
		{"tcp router", map[string]any{"labels": map[string]any{"traefik.tcp.routers.x.rule": "HostSNI(`*`)"}}, nil},
		{"udp router", map[string]any{"labels": map[string]any{"traefik.udp.routers.x.entrypoints": "websecure"}}, nil},
		{"network_mode another compose's network", map[string]any{"network_mode": "relist-c2_default"}, nil},
		{"network_mode bridge", map[string]any{"network_mode": "bridge"}, nil},
		{"built image named like another's", map[string]any{"image": "postgres:16", "build": map[string]any{"context": filepath.Join(f.dir, "src")}}, nil},
		{"built image in a registry", map[string]any{"image": "ghcr.io/example/app:1", "build": map[string]any{"context": filepath.Join(f.dir, "src")}}, nil},
		{"built image of a longer project name", map[string]any{"image": "relist-c12-web", "build": map[string]any{"context": filepath.Join(f.dir, "src")}}, nil},
		{"build tag named like another's", map[string]any{"image": "relist-c1-web", "build": map[string]any{"context": filepath.Join(f.dir, "src"), "tags": []any{"postgres:16"}}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			web := mergeImage(c.web)
			violations, err := Check(config(t, web, c.top), "", f.rules)
			if err != nil {
				t.Fatal(err)
			}
			if len(violations) == 0 {
				t.Fatal("no violation")
			}
			for _, v := range violations {
				if v.Service == "" || v.Message == "" {
					t.Errorf("violation %+v names no service or message", v)
				}
			}
		})
	}
}

func TestAllowedShapes(t *testing.T) {
	f := newFixture(t)
	for name, web := range map[string]map[string]any{
		"bind in a root":                    {"volumes": []any{bind(filepath.Join(f.rules.Roots[0], "media"))}},
		"same bind twice":                   {"volumes": []any{bind(filepath.Join(f.dir, "data")), map[string]any{"type": "bind", "source": filepath.Join(f.dir, "data"), "target": "/again"}}},
		"privileged false":                  {"privileged": false},
		"network of another service":        {"network_mode": "service:db"},
		"volumes_from a service":            {"volumes_from": []any{"db:ro"}},
		"build in the source":               {"build": map[string]any{"context": filepath.Join(f.dir, "src"), "dockerfile": "Dockerfile", "additional_contexts": map[string]any{"base": "docker-image://alpine", "dep": "service:db"}}},
		"build from git":                    {"build": map[string]any{"context": "https://github.com/example/app.git#main", "dockerfile": "Dockerfile"}},
		"traefik off":                       {"labels": map[string]any{"traefik.enable": "false"}},
		"traefik off on the shared network": {"labels": map[string]any{"Traefik.Enable": "0", "traefik.docker.network": "relist-network"}},
		"the panel's own Domain labels":     {"labels": map[string]any{"traefik.enable": "true", "traefik.http.routers.c1-web.rule": "Host(`app.example.com`)"}},
		"a build label":                     {"build": map[string]any{"context": filepath.Join(f.dir, "src"), "labels": map[string]any{"org.opencontainers.image.title": "app"}}},
		"network_mode none":                 {"network_mode": "none"},
		"built image in its namespace":      {"image": "relist-c1-web:latest", "build": map[string]any{"context": filepath.Join(f.dir, "src"), "tags": []any{"relist-c1-web:v2"}}},
		"bind to a missing file":            {"volumes": []any{bind(filepath.Join(f.dir, "src", "not-yet", "file"))}},
	} {
		t.Run(name, func(t *testing.T) {
			doc := map[string]any{"name": "relist-c1", "services": map[string]any{"web": mergeImage(web), "db": map[string]any{"image": "postgres"}}}
			raw, _ := json.Marshal(doc)
			violations, err := Check(raw, "", f.rules)
			if err != nil || len(violations) != 0 {
				t.Fatalf("violations = %+v, err = %v", violations, err)
			}
		})
	}
}

// mergeImage gives a service that builds nothing an image, as config does;
// a built service without image gets none.
func mergeImage(web map[string]any) map[string]any {
	out := map[string]any{}
	if _, builds := web["build"]; !builds {
		out["image"] = "nginx"
	}
	for k, v := range web {
		out[k] = v
	}
	return out
}

func TestStoredIncludeAndExtends(t *testing.T) {
	f := newFixture(t)
	raw := config(t, map[string]any{"image": "nginx"}, nil)
	for stored, wantViolation := range map[string]bool{
		"include: [other.yml]\nservices: {web: {image: nginx}}":                            false,
		"include: [{path: [sub/a.yml]}]\nservices: {web: {image: nginx}}":                  false,
		"services: {web: {extends: {file: base.yml, service: web}}}":                       false,
		"services: {web: {extends: web2}, web2: {image: nginx}}":                           false,
		"include: [/etc/compose.yml]\nservices: {web: {image: nginx}}":                     true,
		"include: [../other/compose.yml]\nservices: {web: {image: nginx}}":                 true,
		"include: ['${DIR}/compose.yml']\nservices: {web: {image: nginx}}":                 true,
		"include: ['oci://registry.example.com/app:1']\nservices: {web: {image: nginx}}":   true,
		"include: [{path: /etc/x.yml}]\nservices: {web: {image: nginx}}":                   true,
		"services: {web: {extends: {file: /srv/base.yml, service: web}}}":                  true,
		"services: {web: {extends: {file: 'https://example.com/base.yml', service: web}}}": true,
	} {
		violations, err := Check(raw, stored, f.rules)
		if err != nil {
			t.Fatalf("%q: %v", stored, err)
		}
		if got := len(violations) > 0; got != wantViolation {
			t.Errorf("%q: violations = %+v, want violation %v", stored, violations, wantViolation)
		}
	}
}

// A violation names the service, the key and the path, never a value an
// environment or label carries.
func TestViolationsCarryNoValues(t *testing.T) {
	f := newFixture(t)
	web := map[string]any{
		"image":       "nginx",
		"environment": map[string]any{"TOKEN": "s3cr3t"},
		"labels":      map[string]any{"traefik.http.routers.x.rule": "Host(`panel.example.com`) && s3cr3t"},
		"made_up":     "s3cr3t",
	}
	violations, err := Check(config(t, web, nil), "", f.rules)
	if err != nil || len(violations) == 0 {
		t.Fatalf("violations = %+v, err = %v", violations, err)
	}
	for _, v := range violations {
		if strings.Contains(v.Message, "s3cr3t") {
			t.Errorf("message carries a value: %q", v.Message)
		}
	}
}

func TestHashIgnoresKeyOrder(t *testing.T) {
	a, err := Hash([]byte(`{"services":{"web":{"image":"nginx","restart":"always"}},"name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Hash([]byte(`{"name":"x","services":{"web":{"restart":"always","image":"nginx"}}}`))
	c, _ := Hash([]byte(`{"name":"x","services":{"web":{"restart":"no","image":"nginx"}}}`))
	if a != b || a == c || len(a) != 64 {
		t.Fatalf("hashes %s %s %s", a, b, c)
	}
}

// Final review: the panel's own files, links whose target the panel cannot
// see, nested keys and networks.
func TestReviewViolations(t *testing.T) {
	f := newFixture(t)
	dangling := filepath.Join(f.dir, "data", "link")
	if err := os.Symlink("/no/such/dir/in/the/panel", dangling); err != nil {
		t.Fatal(err)
	}
	net := func(def map[string]any) map[string]any {
		return map[string]any{"networks": map[string]any{"n": def}}
	}
	cases := []struct {
		name string
		web  map[string]any
		top  map[string]any
	}{
		{"deployment sources", map[string]any{"volumes": []any{bind(filepath.Join(f.dir, ".deployment-sources"))}}, nil},
		{"env file", map[string]any{"volumes": []any{bind(filepath.Join(f.dir, ".env"))}}, nil},
		{"deployed compose file", map[string]any{"volumes": []any{bind(filepath.Join(f.dir, "docker-compose.yml"))}}, nil},
		{"sets", map[string]any{"volumes": []any{bind(filepath.Join(f.dir, "sets"))}}, nil},
		{"deployment logs", map[string]any{"volumes": []any{bind(filepath.Join(f.dir, ".deployments"))}}, nil},
		{"dangling link", map[string]any{"volumes": []any{bind(dangling)}}, nil},
		{"deploy devices", map[string]any{"deploy": map[string]any{"resources": map[string]any{"reservations": map[string]any{"devices": []any{map[string]any{"driver": "cdi", "device_ids": []any{"nvidia.com/gpu=all"}}}}}}}, nil},
		{"unknown deploy key", map[string]any{"deploy": map[string]any{"made_up": 1}}, nil},
		{"build cache_to local", map[string]any{"build": map[string]any{"context": filepath.Join(f.dir, "src"), "cache_to": []any{"type=local,dest=/etc"}}}, nil},
		{"build cache_from local", map[string]any{"build": map[string]any{"context": filepath.Join(f.dir, "src"), "cache_from": []any{"type=local,src=/root"}}}, nil},
		{"unknown build key", map[string]any{"build": map[string]any{"context": filepath.Join(f.dir, "src"), "made_up": true}}, nil},
		{"external network", map[string]any{"networks": map[string]any{"n": nil}}, net(map[string]any{"name": "relist-c2_default", "external": true})},
		{"host network driver", map[string]any{"networks": map[string]any{"n": nil}}, net(map[string]any{"name": "relist-c1_n", "driver": "host"})},
		{"network driver_opts", map[string]any{"networks": map[string]any{"n": nil}}, net(map[string]any{"name": "relist-c1_n", "driver_opts": map[string]any{"com.docker.network.bridge.name": "docker0"}})},
		{"foreign network name", map[string]any{"networks": map[string]any{"n": nil}}, net(map[string]any{"name": "relist-c2_default"})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			violations, err := Check(config(t, mergeImage(c.web), c.top), "", f.rules)
			if err != nil || len(violations) == 0 {
				t.Fatalf("violations = %+v, err = %v", violations, err)
			}
		})
	}
}

// A Deployment's own private Source copy is where its binds point; it is
// allowed, while another copy is not.
func TestOwnSourceCopyIsAllowed(t *testing.T) {
	f := newFixture(t)
	own := filepath.Join(f.dir, ".deployment-sources", "source-a")
	if err := os.MkdirAll(filepath.Join(own, "src", "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	rules := f.rules
	rules.SourceCopy = own
	web := mergeImage(map[string]any{
		"volumes": []any{bind(filepath.Join(own, "src", "conf"))},
		"build":   map[string]any{"context": filepath.Join(own, "src"), "cache_from": []any{"type=registry,ref=x"}},
	})
	if violations, _ := Check(config(t, web, nil), "", rules); len(violations) != 0 {
		t.Fatalf("own copy: %+v", violations)
	}
	other := mergeImage(map[string]any{"volumes": []any{bind(filepath.Join(f.dir, ".deployment-sources", "source-b", "src"))}})
	if violations, _ := Check(config(t, other, nil), "", rules); len(violations) == 0 {
		t.Fatal("another Deployment's copy was allowed")
	}
}

// An include reached through a Source file is checked like the top file's.
func TestIncludeInsideASourceFile(t *testing.T) {
	f := newFixture(t)
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(f.dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/a.yml", "include:\n  - path: b.yml\n    env_file: /var/lib/relist-panel/.composes/2/.env\n")
	write("src/ok.yml", "include: [c.yml]\nservices: {x: {extends: {file: d.yml, service: x}}}\n")
	raw := config(t, map[string]any{"image": "nginx"}, nil)
	if violations, _ := Check(raw, "include: [src/a.yml]\nservices: {web: {image: nginx}}", f.rules); len(violations) == 0 {
		t.Fatal("a nested include reading another Compose's .env passed")
	}
	if violations, _ := Check(raw, "include: [src/ok.yml]\nservices: {web: {image: nginx}}", f.rules); len(violations) != 0 {
		t.Fatalf("nested relative includes: %+v", violations)
	}
}

// A com.docker.* label would make a container, volume or network pass as
// another Compose's: the panel finds them by com.docker.compose.project.
func TestDockerLabelsAreRefused(t *testing.T) {
	f := newFixture(t)
	for name, c := range map[string]struct{ web, top map[string]any }{
		"service": {map[string]any{"labels": map[string]any{"com.docker.compose.project": "relist-c2"}}, nil},
		"volume":  {map[string]any{"volumes": []any{map[string]any{"type": "volume", "source": "v", "target": "/v"}}}, map[string]any{"volumes": map[string]any{"v": map[string]any{"name": "relist-c1_v", "labels": map[string]any{"com.docker.compose.project": "relist-c2"}}}}},
		"network": {map[string]any{"networks": map[string]any{"n": nil}}, map[string]any{"networks": map[string]any{"n": map[string]any{"name": "relist-c1_n", "labels": map[string]any{"com.docker.compose.project": "relist-c2"}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			if violations, _ := Check(config(t, mergeImage(c.web), c.top), "", f.rules); len(violations) == 0 {
				t.Fatal("no violation")
			}
		})
	}
	ok := mergeImage(map[string]any{"labels": map[string]any{"com.example.owner": "panel.example.com"}})
	if violations, _ := Check(config(t, ok, nil), "", f.rules); len(violations) != 0 {
		t.Fatalf("an ordinary label: %+v", violations)
	}
}

func TestHandWrittenTraefikLabelPointsToDomains(t *testing.T) {
	f := newFixture(t)
	web := map[string]any{"image": "nginx", "labels": map[string]any{"traefik.http.routers.x.rule": "Host(`app.example.com`)"}}
	violations, err := Check(config(t, web, nil), "", f.rules)
	if err != nil || len(violations) != 1 {
		t.Fatalf("violations = %+v, err = %v", violations, err)
	}
	if !strings.Contains(violations[0].Message, "Domains tab") {
		t.Errorf("message %q does not point to the Domains tab", violations[0].Message)
	}
}

// The panel reaches its Registry as relist-registry on the shared network and
// sends the system login there, so no Compose may answer to that name on it.
func TestRegistryNameOnTheSharedNetworkIsRefused(t *testing.T) {
	f := newFixture(t)
	shared := func(extra map[string]any) map[string]any {
		web := map[string]any{"networks": map[string]any{"relist-network": extra}}
		return mergeImage(web)
	}
	for name, doc := range map[string]map[string]any{
		"alias":          {"web": shared(map[string]any{"aliases": []any{"relist-registry"}})},
		"alias any case": {"web": shared(map[string]any{"aliases": []any{"Relist-Registry."}})},
		"container name": {"web": mergeImage(map[string]any{"container_name": "relist-registry", "networks": map[string]any{"relist-network": nil}})},
		"service name":   {"relist-registry": shared(nil)},
	} {
		t.Run(name, func(t *testing.T) {
			top := ordinaryTop()
			top["name"] = "relist-c1"
			top["services"] = doc
			raw, _ := json.Marshal(top)
			violations, err := Check(raw, "", f.rules)
			if err != nil || len(violations) == 0 {
				t.Fatalf("violations = %+v, err = %v", violations, err)
			}
			if !strings.Contains(violations[0].Message, "relist-registry") {
				t.Errorf("message %q does not name relist-registry", violations[0].Message)
			}
		})
	}
}

// The name is the panel's only on the shared network: a Compose's own
// network may use it.
func TestRegistryNameOnAnOwnNetworkPasses(t *testing.T) {
	f := newFixture(t)
	doc := map[string]any{"name": "relist-c1",
		"services": map[string]any{"relist-registry": map[string]any{"image": "registry:3", "networks": map[string]any{"default": map[string]any{"aliases": []any{"relist-registry"}}}}},
		"networks": map[string]any{"default": map[string]any{"name": "relist-c1_default"}}}
	raw, _ := json.Marshal(doc)
	violations, err := Check(raw, "", f.rules)
	if err != nil || len(violations) != 0 {
		t.Fatalf("violations = %+v, err = %v", violations, err)
	}
}
