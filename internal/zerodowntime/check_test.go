package zerodowntime

import (
	"os"
	"reflect"
	"testing"
	"time"
)

// testdata/config.json is real `docker compose config --format json` output
// (Compose v5.5.1) of a file with PORT=8080 and N=2 in its .env, so values
// written as ${PORT} and ${N} arrive interpolated.
func readConfig(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/config.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCheck(t *testing.T) {
	config := readConfig(t)
	tests := []struct {
		service string
		reasons []string
	}{
		{"ok", nil},
		{"defaults", nil},
		{"nohc", []string{"has no healthcheck"}},
		{"disabled", []string{"has no healthcheck"}},
		{"none", []string{"has no healthcheck"}},
		{"named", []string{"sets container_name; two Containers can't share a name"}},
		{"hostport", []string{"publishes host port 8080; two Containers can't bind it"}},
		{"longport", []string{"publishes host port 9000; two Containers can't bind it"}},
		{"replicas", []string{"runs more than one replica; rollout supports one"}},
		{"scaled", []string{"runs more than one replica; rollout supports one"}},
		{"anon", []string{"has an anonymous volume at /data; mount a named volume there"}},
		{"two", []string{
			"has no healthcheck",
			"sets container_name; two Containers can't share a name",
			"has an anonymous volume at /cache; mount a named volume there",
		}},
		{"renamed-away", nil},
	}
	for _, tc := range tests {
		t.Run(tc.service, func(t *testing.T) {
			got, err := Check(config, []string{tc.service})
			if err != nil {
				t.Fatal(err)
			}
			var want []Problem
			if tc.reasons != nil {
				want = []Problem{{Service: tc.service, Reasons: tc.reasons}}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Check(%s) = %+v, want %+v", tc.service, got, want)
			}
		})
	}
}

func TestCheckKeepsServiceOrder(t *testing.T) {
	got, err := Check(readConfig(t), []string{"nohc", "ok", "named"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Service != "nohc" || got[1].Service != "named" {
		t.Fatalf("Check = %+v", got)
	}
}

func TestCheckRejectsBadJSON(t *testing.T) {
	if _, err := Check([]byte("not json"), []string{"web"}); err == nil {
		t.Fatal("want an error")
	}
}

func TestTraefikLabels(t *testing.T) {
	got, err := TraefikLabels(readConfig(t), "ok")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"traefik.enable": "true", "traefik.http.routers.x.rule": "Host(`a.test`)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TraefikLabels = %v, want %v", got, want)
	}
}

func TestWaitLimit(t *testing.T) {
	config := readConfig(t)
	for service, want := range map[string]time.Duration{
		"ok":       37 * time.Second,               // 15s + (2s + 2s) × 3 + 10s
		"defaults": 3*time.Minute + 10*time.Second, // 0 + (30s + 30s) × 3 + 10s
	} {
		got, err := WaitLimit(config, service)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("WaitLimit(%s) = %s, want %s", service, got, want)
		}
	}
}

// compose config writes a literal $ back as $$; the running Container's label
// holds one $. Comparing the two must not see drift.
func TestTraefikLabelsUnescapesDollar(t *testing.T) {
	config := []byte(`{"services":{"web":{"labels":{"traefik.http.middlewares.auth.basicauth.users":"u:$$apr1$$abc","traefik.http.routers.r.rule":"PathRegexp(` + "`^/a$$`" + `)"}}}}`)
	got, err := TraefikLabels(config, "web")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"traefik.http.middlewares.auth.basicauth.users": "u:$apr1$abc", "traefik.http.routers.r.rule": "PathRegexp(`^/a$`)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TraefikLabels = %v, want %v", got, want)
	}
}

// Docker reads retries: 0 as its default of 3, so the wait must too.
func TestWaitLimitZeroRetriesIsDefault(t *testing.T) {
	config := []byte(`{"services":{"web":{"healthcheck":{"test":["CMD","true"],"interval":"20s","timeout":"5s","retries":0}}}}`)
	got, err := WaitLimit(config, "web")
	if err != nil {
		t.Fatal(err)
	}
	if want := 85 * time.Second; got != want { // 0 + (20s + 5s) × 3 + 10s
		t.Fatalf("WaitLimit = %s, want %s", got, want)
	}
}
