package panelupdate

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

const fixtureImage = "ghcr.io/relist/relist-panel@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func fixture(t *testing.T, name string) *composeConfig {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	c, err := parseComposeConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func inv(t *testing.T, fields map[string]any) Invariant {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	var i Invariant
	if err := json.Unmarshal(raw, &i); err != nil {
		t.Fatal(err)
	}
	return i
}

type m = map[string]any

func TestInvariantKinds(t *testing.T) {
	want := []string{"data-dir-same-path", "network-external", "service-command-has", "service-image", "service-label", "service-mount", "service-network"}
	if got := InvariantKinds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds = %v", got)
	}
	got := UnknownKinds([]Invariant{inv(t, m{"kind": "service-image"}), inv(t, m{"kind": "new"}), inv(t, m{"kind": "new"})})
	if !reflect.DeepEqual(got, []string{"new"}) {
		t.Fatalf("unknown = %v", got)
	}
}

func TestInvariantEvaluators(t *testing.T) {
	base := fixture(t, "config_base.json")
	build := fixture(t, "config_panel_build.json")
	override := fixture(t, "config_override_command.json")
	env := invariantEnv{Image: fixtureImage, Vars: map[string]string{"RELIST_DATA_DIR": "/var/lib/relist-panel"}}
	cases := []struct {
		name string
		cfg  *composeConfig
		inv  m
		ok   bool
	}{
		{"image holds", base, m{"kind": "service-image", "service": "panel", "image": fixtureImage}, true},
		{"image defaults to the manifest's", base, m{"kind": "service-image", "service": "panel"}, true},
		{"image differs", base, m{"kind": "service-image", "service": "panel", "image": "other@sha256:1"}, false},
		{"image with build", build, m{"kind": "service-image", "service": "panel", "image": "ghcr.io/relist/relist-panel:local"}, false},
		{"image of an unknown service", base, m{"kind": "service-image", "service": "nope"}, false},
		{"mount holds", base, m{"kind": "service-mount", "service": "panel", "source": "/var/run/docker.sock", "target": "/var/run/docker.sock"}, true},
		{"mount read-only", base, m{"kind": "service-mount", "service": "panel", "source": "/proc", "target": "/host/proc", "read_only": true}, true},
		{"mount should be read-only", base, m{"kind": "service-mount", "service": "panel", "source": "/var/run/docker.sock", "target": "/var/run/docker.sock", "read_only": true}, false},
		{"mount expands variables", base, m{"kind": "service-mount", "service": "panel", "source": "${RELIST_DATA_DIR:-/x}", "target": "${RELIST_DATA_DIR}"}, true},
		{"mount expands defaults", base, m{"kind": "service-mount", "service": "traefik", "source": "${NOPE:-/var/lib/relist-panel}/.traefik", "target": "/letsencrypt"}, true},
		{"mount missing", base, m{"kind": "service-mount", "service": "panel", "source": "/nope", "target": "/nope"}, false},
		{"command has", base, m{"kind": "service-command-has", "service": "traefik", "arg": "--providers.file.directory=/letsencrypt/dynamic"}, true},
		{"command lacks", base, m{"kind": "service-command-has", "service": "traefik", "arg": "--entrypoints.websecure.address"}, false},
		{"command replaced by an override", override, m{"kind": "service-command-has", "service": "traefik", "arg": "--providers.file.directory=/letsencrypt/dynamic"}, false},
		{"command of a service with none", base, m{"kind": "service-command-has", "service": "panel", "arg": "x"}, false},
		{"label holds", base, m{"kind": "service-label", "service": "panel", "key": "traefik.http.routers.panel.priority", "value": "100000"}, true},
		{"label differs", base, m{"kind": "service-label", "service": "panel", "key": "traefik.http.routers.panel.priority", "value": "1"}, false},
		{"label missing", base, m{"kind": "service-label", "service": "panel", "key": "nope", "value": ""}, false},
		{"network holds", base, m{"kind": "service-network", "service": "traefik", "network": "relist-network"}, true},
		{"network missing", base, m{"kind": "service-network", "service": "traefik", "network": "other"}, false},
		{"external holds", base, m{"kind": "network-external", "network": "relist-network", "name": "relist-network"}, true},
		{"external with another name", base, m{"kind": "network-external", "network": "relist-network", "name": "other"}, false},
		{"external of an unknown network", base, m{"kind": "network-external", "network": "nope", "name": "nope"}, false},
		{"data dir holds", base, m{"kind": "data-dir-same-path", "service": "panel"}, true},
		{"data dir not mounted", override, m{"kind": "data-dir-same-path", "service": "traefik"}, false},
		{"malformed", base, m{"kind": "service-mount", "service": 3}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := evaluate(inv(t, c.inv), c.cfg, env)
			if o.OK != c.ok || o.Text == "" || (!o.OK && o.Detail == "") {
				t.Fatalf("outcome = %+v, want ok=%v", o, c.ok)
			}
		})
	}
}

func TestNonExternalNetworkFails(t *testing.T) {
	cfg, err := parseComposeConfig([]byte(`{"networks":{"n":{"name":"n"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if o := evaluate(inv(t, m{"kind": "network-external", "network": "n", "name": "n"}), cfg, invariantEnv{}); o.OK {
		t.Fatal("a network that is not external held")
	}
}

func TestListFormsOfLabelsAndEnvironment(t *testing.T) {
	cfg, err := parseComposeConfig([]byte(`{"services":{"s":{"labels":["a=b"],"networks":["n"],"command":"x --y","environment":{"RELIST_DATA_DIR":"/d"},"volumes":[{"source":"/d","target":"/d"}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []m{
		{"kind": "service-label", "service": "s", "key": "a", "value": "b"},
		{"kind": "service-network", "service": "s", "network": "n"},
		{"kind": "service-command-has", "service": "s", "arg": "--y"},
		{"kind": "data-dir-same-path", "service": "s"},
	} {
		if o := evaluate(inv(t, i), cfg, invariantEnv{}); !o.OK {
			t.Errorf("%v: %+v", i, o)
		}
	}
}

func TestExpandVars(t *testing.T) {
	vars := map[string]string{"A": "a", "EMPTY": ""}
	for in, want := range map[string]string{
		"${A}": "a", "x${A}y": "xay", "${B:-d}": "d", "${EMPTY:-d}": "d", "${EMPTY-d}": "", "${B-d}": "d",
		"${B}": "", "$A": "$A", "${A": "${A", "plain": "plain",
	} {
		if got := expandVars(in, vars); got != want {
			t.Errorf("expandVars(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSetEnvKey(t *testing.T) {
	cases := map[string][2]string{
		"empty":           {"", "K=v\n"},
		"append":          {"A=1\nB=2\n", "A=1\nB=2\nK=v\n"},
		"append no eol":   {"A=1", "A=1\nK=v\n"},
		"replace":         {"A=1\nK=old\nB=2\n", "A=1\nK=v\nB=2\n"},
		"replace repeats": {"K=1\nA=1\nK=2\n", "K=v\nA=1\n"},
		"export":          {"export K=old\n", "K=v\n"},
		"comment kept":    {"# K=old\nA=1\n", "# K=old\nA=1\nK=v\n"},
		"prefix not key":  {"KK=1\n", "KK=1\nK=v\n"},
	}
	for name, c := range cases {
		if got := string(setEnvKey([]byte(c[0]), "K", "v")); got != c[1] {
			t.Errorf("%s: got %q, want %q", name, got, c[1])
		}
	}
}

func TestParseEnv(t *testing.T) {
	got := parseEnv([]byte("# c\nA=1\nB = \"two words\"\nC='x'\nexport D=4 # note\n\nbad\n"))
	want := map[string]string{"A": "1", "B": "two words", "C": "x", "D": "4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("env = %v", got)
	}
	if strings.Contains(string(setEnvKey(nil, "A", "b")), "\n\n") {
		t.Fatal("blank line")
	}
}
