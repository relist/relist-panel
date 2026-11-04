package traefikconfig

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func args002(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile("testdata/args-0.0.2.txt")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(b))
}

// effectiveOf generates traefik.yml from override, failing on any problem.
func effectiveOf(t *testing.T, override []byte) []byte {
	t.Helper()
	out, problems, err := Generate(override)
	if err != nil || len(problems) != 0 {
		t.Fatalf("Generate: err = %v, problems = %+v\n%s", err, problems, override)
	}
	return out
}

func TestFromArgs002(t *testing.T) {
	override, warnings := FromArgs(args002(t))
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v", warnings)
	}
	eff := effectiveOf(t, override)
	m, _ := parseMap(eff)
	if _, v := lookup(m, "certificatesResolvers", "letsencrypt", "acme", "email"); v == nil || v.Value != "ops@example.com" {
		t.Errorf("email = %v", v)
	}
	if got := TrustedIPs(eff); len(got) != 2 || got[0] != "10.0.0.0/8" || got[1] != "192.168.0.0/16" {
		t.Errorf("websecure trustedIPs = %v", got)
	}
	_, web := lookup(m, "entryPoints", "web", "forwardedHeaders", "trustedIPs")
	if web == nil || len(web.Content) != 2 {
		t.Errorf("web trustedIPs = %v", web)
	}
	if _, v := lookup(m, "log", "level"); v == nil || v.Value != "INFO" {
		t.Errorf("log level = %v", v)
	}
	if !AccessLogOn(eff) {
		t.Errorf("accessLog missing")
	}
	lower := strings.ToLower(string(override))
	for _, gone := range []string{"address", "readtimeout", "storage", "exposedbydefault", "directory", "nocolor", "filepath", "defaultmode", "httpchallenge"} {
		if strings.Contains(lower, gone) {
			t.Errorf("override repeats the base's %s:\n%s", gone, override)
		}
	}
}

func TestFromArgsChangedFlag(t *testing.T) {
	override, _ := FromArgs([]string{"--log.level=DEBUG"})
	m, err := parseMap(override)
	if err != nil {
		t.Fatal(err)
	}
	if _, v := lookup(m, "log", "level"); v == nil || v.Value != "DEBUG" {
		t.Fatalf("log level = %v in\n%s", v, override)
	}
}

func TestFromArgsUnconvertible(t *testing.T) {
	override, warnings := FromArgs([]string{"--entrypoints.web.address=:8080", "--weird"})
	for _, flag := range []string{"--entrypoints.web.address=:8080", "--weird"} {
		if !strings.Contains(string(override), "# not converted: "+flag) {
			t.Errorf("no comment for %s in\n%s", flag, override)
		}
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %+v", warnings)
	}
	effectiveOf(t, override)
}

func TestFromArgsSkipsConfigFile(t *testing.T) {
	override, warnings := FromArgs([]string{"--configfile=/etc/traefik/traefik.yml"})
	if strings.Contains(strings.ToLower(string(override)), "configfile") || len(warnings) != 0 {
		t.Fatalf("override = %s, warnings = %+v", override, warnings)
	}
}

func TestFromArgsAccessLogFalse(t *testing.T) {
	override, _ := FromArgs([]string{"--accesslog=false"})
	if AccessLogOn(effectiveOf(t, override)) {
		t.Fatalf("accessLog on for --accesslog=false:\n%s", override)
	}
}

func TestFromArgsSectionFlag(t *testing.T) {
	override, _ := FromArgs([]string{"--api=true", "--metrics.prometheus=true"})
	eff := effectiveOf(t, override)
	m, _ := parseMap(eff)
	for _, path := range [][]string{{"api"}, {"metrics", "prometheus"}} {
		if _, v := lookup(m, path...); v == nil || v.Kind != yaml.MappingNode {
			t.Errorf("%v = %v, want an empty map", path, v)
		}
	}
}

func TestSeed(t *testing.T) {
	eff := effectiveOf(t, Seed("a@b.c", "1.2.3.4, 10.0.0.0/8", false))
	m, _ := parseMap(eff)
	if _, v := lookup(m, "certificatesResolvers", "letsencrypt", "acme", "email"); v == nil || v.Value != "a@b.c" {
		t.Errorf("email = %v", v)
	}
	for _, ep := range []string{"web", "websecure"} {
		_, v := lookup(m, "entryPoints", ep, "forwardedHeaders", "trustedIPs")
		if v == nil || len(v.Content) != 2 || v.Content[0].Value != "1.2.3.4" || v.Content[1].Value != "10.0.0.0/8" {
			t.Errorf("%s trustedIPs = %v", ep, v)
		}
	}
	if AccessLogOn(eff) {
		t.Errorf("Seed turned the Access log on")
	}
	empty := Seed("", "", false)
	for _, line := range strings.Split(strings.TrimSpace(string(empty)), "\n") {
		if !strings.HasPrefix(line, "#") {
			t.Fatalf("Seed(\"\", \"\") has a non-comment line %q", line)
		}
	}
	effectiveOf(t, empty)
}
