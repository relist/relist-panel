package domains_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"relist-panel/internal/domains"
)

const network = "relist-network"

func parseServices(t *testing.T, src string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	return doc["services"].(map[string]any)
}

// labelsOf reads a Service's labels back as key to value, in either shape.
func labelsOf(t *testing.T, services map[string]any, name string) map[string]string {
	t.Helper()
	out := map[string]string{}
	switch labels := services[name].(map[string]any)["labels"].(type) {
	case []any:
		for _, item := range labels {
			key, val, _ := strings.Cut(item.(string), "=")
			out[key] = val
		}
	case map[string]any:
		for key, val := range labels {
			s, ok := val.(string)
			if !ok {
				t.Fatalf("label %s is %T, want string", key, val)
			}
			out[key] = s
		}
	default:
		t.Fatalf("labels are %T", labels)
	}
	return out
}

func inject(t *testing.T, src string, ds ...domains.Domain) (map[string]any, []string) {
	t.Helper()
	services := parseServices(t, src)
	warnings, err := domains.Inject(services, 12, ds, network)
	if err != nil {
		t.Fatal(err)
	}
	return services, warnings
}

func assertLabels(t *testing.T, got, want map[string]string) {
	t.Helper()
	for key, val := range want {
		if g, ok := got[key]; !ok || g != val {
			t.Errorf("label %s = %q (present %v), want %q", key, g, ok, val)
		}
	}
}

func assertAbsent(t *testing.T, got map[string]string, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if v, ok := got[key]; ok {
			t.Errorf("label %s = %q, want absent", key, v)
		}
	}
}

const plain = "services:\n  web:\n    image: x\n"

func base() domains.Domain {
	return domains.Domain{ID: 3, Host: "a.example.com", Path: "/", InternalPath: "/", Service: "web", Port: 3000}
}

func TestInjectHTTPOnly(t *testing.T) {
	services, warnings := inject(t, plain, base())
	got := labelsOf(t, services, "web")
	assertLabels(t, got, map[string]string{
		"traefik.enable":                                               "true",
		"traefik.docker.network":                                       network,
		"traefik.http.routers.relist-c12-d3.rule":                      "Host(`a.example.com`)",
		"traefik.http.routers.relist-c12-d3.entrypoints":               "web",
		"traefik.http.routers.relist-c12-d3.service":                   "relist-c12-d3",
		"traefik.http.services.relist-c12-d3.loadbalancer.server.port": "3000",
	})
	assertAbsent(t, got,
		"traefik.http.routers.relist-c12-d3.tls",
		"traefik.http.routers.relist-c12-d3.middlewares",
		"traefik.http.routers.relist-c12-d3-http.rule")
	if len(warnings) != 0 {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestInjectPathPrefix(t *testing.T) {
	d := base()
	d.Path = "/api"
	services, _ := inject(t, plain, d)
	assertLabels(t, labelsOf(t, services, "web"), map[string]string{
		"traefik.http.routers.relist-c12-d3.rule": "Host(`a.example.com`) && PathPrefix(`/api`)",
	})
}

func TestInjectHTTPSLetsEncrypt(t *testing.T) {
	d := base()
	d.HTTPS, d.CertProvider = true, "letsencrypt"
	services, _ := inject(t, plain, d)
	got := labelsOf(t, services, "web")
	rule := "Host(`a.example.com`)"
	assertLabels(t, got, map[string]string{
		"traefik.http.routers.relist-c12-d3.entrypoints":                           "websecure",
		"traefik.http.routers.relist-c12-d3.tls":                                   "true",
		"traefik.http.routers.relist-c12-d3.tls.certresolver":                      "letsencrypt",
		"traefik.http.routers.relist-c12-d3-http.rule":                             rule,
		"traefik.http.routers.relist-c12-d3-http.entrypoints":                      "web",
		"traefik.http.routers.relist-c12-d3-http.middlewares":                      "relist-c12-d3-redirect",
		"traefik.http.routers.relist-c12-d3-http.service":                          "relist-c12-d3",
		"traefik.http.middlewares.relist-c12-d3-redirect.redirectscheme.scheme":    "https",
		"traefik.http.middlewares.relist-c12-d3-redirect.redirectscheme.permanent": "true",
	})
}

func TestInjectHTTPSCustomAndNone(t *testing.T) {
	d := base()
	d.HTTPS, d.CertProvider, d.CertResolver = true, "custom", "myresolver"
	services, _ := inject(t, plain, d)
	assertLabels(t, labelsOf(t, services, "web"), map[string]string{
		"traefik.http.routers.relist-c12-d3.tls.certresolver": "myresolver",
	})

	d.CertProvider, d.CertResolver = "none", ""
	services, _ = inject(t, plain, d)
	got := labelsOf(t, services, "web")
	assertLabels(t, got, map[string]string{"traefik.http.routers.relist-c12-d3.tls": "true"})
	assertAbsent(t, got, "traefik.http.routers.relist-c12-d3.tls.certresolver")
}

func TestInjectCustomEntrypoint(t *testing.T) {
	d := base()
	d.HTTPS, d.CertProvider, d.Entrypoint = true, "letsencrypt", "internal"
	services, _ := inject(t, plain, d)
	got := labelsOf(t, services, "web")
	assertLabels(t, got, map[string]string{"traefik.http.routers.relist-c12-d3.entrypoints": "internal"})
	assertAbsent(t, got,
		"traefik.http.routers.relist-c12-d3-http.rule",
		"traefik.http.middlewares.relist-c12-d3-redirect.redirectscheme.scheme")
}

func TestInjectMiddlewareChain(t *testing.T) {
	d := base()
	d.Path, d.StripPath, d.InternalPath, d.Middlewares = "/api", true, "/v1", []string{"auth@file"}
	services, _ := inject(t, plain, d)
	assertLabels(t, labelsOf(t, services, "web"), map[string]string{
		"traefik.http.routers.relist-c12-d3.middlewares":                    "relist-c12-d3-strip,relist-c12-d3-prefix,auth@file",
		"traefik.http.middlewares.relist-c12-d3-strip.stripprefix.prefixes": "/api",
		"traefik.http.middlewares.relist-c12-d3-prefix.addprefix.prefix":    "/v1",
	})

	d.Path, d.InternalPath, d.Middlewares = "/", "/", nil
	services, _ = inject(t, plain, d)
	got := labelsOf(t, services, "web")
	assertAbsent(t, got,
		"traefik.http.middlewares.relist-c12-d3-strip.stripprefix.prefixes",
		"traefik.http.routers.relist-c12-d3.middlewares")
}

func TestInjectKeepsLabelShape(t *testing.T) {
	list := "services:\n  web:\n    labels:\n      - traefik.enable\n      - traefik.http.routers.mine.rule=PathPrefix(`/x`)\n      - traefik.http.routers.mine.service=mine\n      - other=1\n"
	services, warnings := inject(t, list, base())
	items, ok := services["web"].(map[string]any)["labels"].([]any)
	if !ok {
		t.Fatalf("labels are %T, want []any", services["web"].(map[string]any)["labels"])
	}
	count := 0
	for _, item := range items {
		if strings.HasPrefix(item.(string), "traefik.enable") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("traefik.enable appears %d times in %v", count, items)
	}
	got := labelsOf(t, services, "web")
	assertLabels(t, got, map[string]string{
		"traefik.enable":                    "true",
		"traefik.http.routers.mine.rule":    "PathPrefix(`/x`)",
		"traefik.http.routers.mine.service": "mine",
		"other":                             "1",
	})
	if len(warnings) != 0 {
		t.Errorf("warnings = %v", warnings)
	}

	mapForm := "services:\n  web:\n    labels:\n      traefik.http.routers.mine.rule: Host(`z`)\n      traefik.http.routers.mine.service: mine\n      keep: 2\n"
	services, _ = inject(t, mapForm, base())
	labels, ok := services["web"].(map[string]any)["labels"].(map[string]any)
	if !ok {
		t.Fatalf("labels are %T, want map", services["web"].(map[string]any)["labels"])
	}
	if labels["keep"] != 2 {
		t.Errorf("keep = %#v, want untouched 2", labels["keep"])
	}
	if labels["traefik.http.routers.mine.rule"] != "Host(`z`)" {
		t.Errorf("hand-written rule changed: %v", labels["traefik.http.routers.mine.rule"])
	}

	services, _ = inject(t, plain, base())
	if _, ok := services["web"].(map[string]any)["labels"].([]any); !ok {
		t.Errorf("absent labels became %T, want []any", services["web"].(map[string]any)["labels"])
	}
}

func TestInjectOverridesWithWarnings(t *testing.T) {
	enable := `service "web": traefik.enable=false was overridden to true because a domain points at it`
	for name, src := range map[string]string{
		"map":  "services:\n  web:\n    labels:\n      traefik.enable: false\n",
		"list": "services:\n  web:\n    labels:\n      - traefik.enable=false\n",
	} {
		t.Run(name, func(t *testing.T) {
			services, warnings := inject(t, src, base())
			assertLabels(t, labelsOf(t, services, "web"), map[string]string{"traefik.enable": "true"})
			if !reflect.DeepEqual(warnings, []string{enable}) {
				t.Errorf("warnings = %q", warnings)
			}
		})
	}

	services, warnings := inject(t, "services:\n  web:\n    labels:\n      - traefik.docker.network=other\n", base())
	assertLabels(t, labelsOf(t, services, "web"), map[string]string{"traefik.docker.network": network})
	want := []string{`service "web": traefik.docker.network was "other", overridden to relist-network`}
	if !reflect.DeepEqual(warnings, want) {
		t.Errorf("warnings = %q, want %q", warnings, want)
	}
}

func TestInjectWarnsOnImplicitRouter(t *testing.T) {
	src := "services:\n  web:\n    labels:\n      - traefik.http.routers.mine.rule=Host(`other.example.com`)\n"
	_, warnings := inject(t, src, base())
	want := []string{`service "web": router "mine" has no traefik.http.routers.mine.service label, so it now routes to a domain's service; add one`}
	if !reflect.DeepEqual(warnings, want) {
		t.Errorf("warnings = %q, want %q", warnings, want)
	}

	src += "      - traefik.http.routers.mine.service=mine\n"
	_, warnings = inject(t, src, base())
	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none", warnings)
	}
}

func TestInjectWarnsOnSameHostRouter(t *testing.T) {
	src := "services:\n  web:\n    image: x\n  other:\n    labels:\n      - traefik.http.routers.old.rule=Host(`a.example.com`)\n      - traefik.http.routers.old.service=old\n"
	_, warnings := inject(t, src, base())
	want := []string{"router \"old\" also matches Host(`a.example.com`), which a domain manages"}
	if !reflect.DeepEqual(warnings, want) {
		t.Errorf("warnings = %q, want %q", warnings, want)
	}
}

func TestInjectTwoDomainsOneService(t *testing.T) {
	second := base()
	second.ID, second.Host, second.Port = 4, "b.example.com", 9000
	services, _ := inject(t, plain, second, base()) // out of order on purpose
	assertLabels(t, labelsOf(t, services, "web"), map[string]string{
		"traefik.http.routers.relist-c12-d3.service":                   "relist-c12-d3",
		"traefik.http.routers.relist-c12-d4.service":                   "relist-c12-d4",
		"traefik.http.services.relist-c12-d3.loadbalancer.server.port": "3000",
		"traefik.http.services.relist-c12-d4.loadbalancer.server.port": "9000",
	})
}

func TestInjectRefusesMissingOrNetworkModeService(t *testing.T) {
	src := "services:\n  web:\n    image: x\n  host:\n    network_mode: host\n"
	missing := base()
	missing.Service = "gone"
	hosted := base()
	hosted.Service = "host"
	for _, tc := range []struct {
		domain domains.Domain
		want   string
	}{
		{missing, `domain a.example.com/ points at service "gone", which is not in the compose file`},
		{hosted, `domain a.example.com/ points at service "host", which sets network_mode and cannot take a domain`},
	} {
		services := parseServices(t, src)
		_, err := domains.Inject(services, 12, []domains.Domain{base(), tc.domain}, network)
		var target *domains.TargetError
		if !errors.As(err, &target) {
			t.Fatalf("err = %v, want *TargetError", err)
		}
		if err.Error() != tc.want {
			t.Errorf("err = %q, want %q", err.Error(), tc.want)
		}
		if _, ok := services["web"].(map[string]any)["labels"]; ok {
			t.Error("services were mutated despite the refusal")
		}
	}
}
