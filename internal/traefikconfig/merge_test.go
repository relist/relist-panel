package traefikconfig

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// generated parses the output of Generate, failing the test on any problem.
func generated(t *testing.T, override string) map[string]any {
	t.Helper()
	out, problems, err := Generate([]byte(override))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %+v", problems)
	}
	var m map[string]any
	if err := yaml.Unmarshal(out, &m); err != nil {
		t.Fatalf("parse output: %v\n%s", err, out)
	}
	return m
}

// at walks m by keys.
func at(t *testing.T, m map[string]any, keys ...string) any {
	t.Helper()
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("%v: not a map at %q", keys, k)
		}
		cur, ok = mm[k]
		if !ok {
			t.Fatalf("%v: no key %q in %v", keys, k, mm)
		}
	}
	return cur
}

func TestGenerateEmptyOverride(t *testing.T) {
	out, problems, err := Generate(nil)
	if err != nil || len(problems) != 0 {
		t.Fatalf("err = %v, problems = %+v", err, problems)
	}
	if first, _, _ := strings.Cut(string(out), "\n"); first != GeneratedHeader {
		t.Fatalf("first line = %q", first)
	}
	m := generated(t, "")
	if got := at(t, m, "entryPoints", "web", "address"); got != ":80" {
		t.Errorf("web address = %v", got)
	}
	if got := at(t, m, "log", "level"); got != "INFO" {
		t.Errorf("log level = %v", got)
	}
	if got := at(t, m, "log", "noColor"); got != true {
		t.Errorf("noColor = %v", got)
	}
	if _, ok := m["accessLog"]; ok {
		t.Errorf("accessLog present without the override asking for it")
	}
}

func TestGenerateAccessLogConditional(t *testing.T) {
	m := generated(t, "accessLog: {}\n")
	if got := at(t, m, "accessLog", "filePath"); got != "/letsencrypt/access/access.log" {
		t.Errorf("filePath = %v", got)
	}
	if got := at(t, m, "accessLog", "format"); got != "json" {
		t.Errorf("format = %v", got)
	}
	if got := at(t, m, "accessLog", "fields", "headers", "defaultMode"); got != "drop" {
		t.Errorf("defaultMode = %v", got)
	}
}

func TestMergeOverrideWins(t *testing.T) {
	if got := at(t, generated(t, "log: {level: DEBUG}\n"), "log", "level"); got != "DEBUG" {
		t.Errorf("level = %v", got)
	}
	m := generated(t, "entryPoints: {web: {forwardedHeaders: {trustedIPs: [10.0.0.0/8]}}}\n")
	if got := at(t, m, "entryPoints", "web", "address"); got != ":80" {
		t.Errorf("address = %v", got)
	}
	ips := at(t, m, "entryPoints", "web", "forwardedHeaders", "trustedIPs").([]any)
	if len(ips) != 1 || ips[0] != "10.0.0.0/8" {
		t.Errorf("trustedIPs = %v", ips)
	}
}

func TestMergeListsReplace(t *testing.T) {
	doc := func(s string) *yaml.Node {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(s), &n); err != nil {
			t.Fatal(err)
		}
		return n.Content[0]
	}
	base := doc("a: [1, 2]\nb: y\n")
	mergeNodes(base, doc("a: [3]\nb: x\n"))
	var got map[string]any
	if err := base.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if a := got["a"].([]any); len(a) != 1 || a[0] != 3 {
		t.Errorf("a = %v", a)
	}
	if got["b"] != "x" {
		t.Errorf("b = %v", got["b"])
	}
}

func TestMergeCaseInsensitive(t *testing.T) {
	out, problems, err := Generate([]byte("entrypoints: {WEB: {forwardedheaders: {trustedips: [1.2.3.4]}}}\n"))
	if err != nil || len(problems) != 0 {
		t.Fatalf("err = %v, problems = %+v", err, problems)
	}
	if bytes.Contains(out, []byte("entrypoints:")) {
		t.Fatalf("lower-case key emitted:\n%s", out)
	}
	if n := bytes.Count(bytes.ToLower(out), []byte("entrypoints:")); n != 1 {
		t.Fatalf("entryPoints appears %d times:\n%s", n, out)
	}
	// Keys the base does not have keep the override's spelling (spec); Traefik
	// ignores case, so the lookup does too.
	m, err := parseMap(out)
	if err != nil {
		t.Fatal(err)
	}
	_, v := lookup(m, "entryPoints", "web", "forwardedHeaders", "trustedIPs")
	if v == nil || len(v.Content) != 1 || v.Content[0].Value != "1.2.3.4" {
		t.Errorf("trustedIPs = %v", v)
	}
}

func TestMergeCaseVariantLockedKeyRefused(t *testing.T) {
	out, problems, err := Generate([]byte(`entrypoints: {web: {Address: ":8080"}}` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		t.Errorf("file returned despite a problem")
	}
	if len(problems) != 1 || problems[0].Key != "entryPoints.web.address" || problems[0].Line != 1 {
		t.Fatalf("problems = %+v", problems)
	}
}

// refused fails unless Generate refuses override with a problem whose message
// contains want.
func refused(t *testing.T, override, want string) {
	t.Helper()
	out, problems, err := Generate([]byte(override))
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		t.Errorf("%q: file returned despite a problem:\n%s", override, out)
	}
	for _, p := range problems {
		if strings.Contains(p.Message, want) {
			return
		}
	}
	t.Fatalf("%q: problems = %+v, want one containing %q", override, problems, want)
}

// Two keys Traefik reads as one: the last wins in the merge, so a locked check
// on the first would pass while the second changes the value.
func TestDuplicateCaseVariantKeysRefused(t *testing.T) {
	refused(t, "providers:\n  docker: {exposedByDefault: false, exposedbydefault: true}\n", "same key")
	refused(t, "entryPoints: {web: {}}\nentrypoints: {web: {address: \":81\"}}\n", "same key")
	refused(t, "entryPoints: {web: {address: \":80\", Address: \":81\"}}\n", "same key")
	refused(t, "log: {level: DEBUG}\nLog: {level: ERROR}\n", "same key")
}

// A scalar where the base has a map replaces the whole map, locked keys included.
func TestScalarOverLockedMapRefused(t *testing.T) {
	refused(t, "providers: {docker: true}\n", "providers.docker")
	refused(t, "providers: true\n", "providers")
	refused(t, "certificatesResolvers: {letsencrypt: {acme: x}}\n", "certificatesResolvers.letsencrypt.acme")
}

// Paths are case-sensitive on Linux: /LETSENCRYPT is off the mounted volume.
func TestLockedPathComparedExactly(t *testing.T) {
	refused(t, "certificatesResolvers: {letsencrypt: {acme: {storage: /LETSENCRYPT/acme.json}}}\n", "the panel needs")
	generated(t, "providers: {docker: {exposedByDefault: FALSE}}\n")
}

func TestLockedKeySameValueAllowed(t *testing.T) {
	generated(t, "log: {noColor: true}\n")
}

func TestUnknownTopLevelRefused(t *testing.T) {
	_, problems, err := Generate([]byte("bogusTop: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || problems[0].Key != "bogusTop" || !strings.Contains(problems[0].Message, "not a Traefik static config section") {
		t.Fatalf("problems = %+v", problems)
	}
}

func TestKnownSectionsAccepted(t *testing.T) {
	generated(t, "api: {insecure: false}\nmetrics: {}\nexperimental: {}\n")
}

func TestBadTrustedIPRefused(t *testing.T) {
	_, problems, err := Generate([]byte(`entryPoints: {websecure: {forwardedHeaders: {trustedIPs: ["not-an-ip"]}}}` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || problems[0].Key != "entryPoints.websecure.forwardedHeaders.trustedIPs" {
		t.Fatalf("problems = %+v", problems)
	}
}

func TestGenerateBadYAML(t *testing.T) {
	if _, _, err := Generate([]byte("a: [oops\n")); err == nil {
		t.Fatal("no error for bad YAML")
	}
}

func TestAccessLogOnAndTrustedIPs(t *testing.T) {
	off, _, _ := Generate(nil)
	if AccessLogOn(off) {
		t.Error("AccessLogOn(no accessLog) = true")
	}
	on, _, err := Generate([]byte("accessLog: {}\nentryPoints: {websecure: {forwardedHeaders: {trustedIPs: [10.0.0.0/8, 1.2.3.4]}}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !AccessLogOn(on) {
		t.Error("AccessLogOn = false")
	}
	if got := TrustedIPs(on); len(got) != 2 || got[0] != "10.0.0.0/8" || got[1] != "1.2.3.4" {
		t.Errorf("TrustedIPs = %v", got)
	}
	if got := TrustedIPs(off); len(got) != 0 {
		t.Errorf("TrustedIPs(none) = %v", got)
	}
}

func TestAccessLogFiltered(t *testing.T) {
	cases := []struct {
		name, override string
		want           bool
	}{
		{"base access log", "accessLog: {}\n", false},
		{"no access log", "", false},
		{"filters", "accessLog: {filters: {statusCodes: ['500']}}\n", true},
		{"router name dropped", "accessLog: {fields: {names: {RouterName: drop}}}\n", true},
		{"all fields dropped by default", "accessLog: {fields: {defaultMode: drop}}\n", true},
		{"other field dropped", "accessLog: {fields: {names: {ClientAddr: drop}}}\n", false},
		{"headers dropped only", "accessLog: {fields: {headers: {defaultMode: drop}}}\n", false},
	}
	for _, c := range cases {
		out, _, err := Generate([]byte(c.override))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := AccessLogFiltered(out); got != c.want {
			t.Errorf("%s: AccessLogFiltered = %v, want %v", c.name, got, c.want)
		}
	}
}
