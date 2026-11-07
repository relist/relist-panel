package registryauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func readConfig(t *testing.T, dir string) map[string]map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Auths map[string]map[string]string `json:"auths"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("config.json %s: %v", raw, err)
	}
	return cfg.Auths
}

func TestWriteConfigDockerHubKey(t *testing.T) {
	dir := t.TempDir()
	err := WriteConfig(dir, []Auth{{Host: "docker.io", Username: "me", Secret: "tok"}, {Host: "ghcr.io", Username: "ci", Secret: "p:w"}})
	if err != nil {
		t.Fatal(err)
	}
	auths := readConfig(t, dir)
	want := map[string]map[string]string{
		"https://index.docker.io/v1/": {"auth": base64.StdEncoding.EncodeToString([]byte("me:tok"))},
		"ghcr.io":                     {"auth": base64.StdEncoding.EncodeToString([]byte("ci:p:w"))},
	}
	if !reflect.DeepEqual(auths, want) {
		t.Fatalf("auths = %v, want %v", auths, want)
	}
}

func TestWriteConfigMode0600(t *testing.T) {
	dir := t.TempDir()
	if err := WriteConfig(dir, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v", info.Mode(), err)
	}
	if auths := readConfig(t, dir); len(auths) != 0 {
		t.Fatalf("auths = %v, want none", auths)
	}
}

type fixed []Auth

func (f fixed) Auths(context.Context) ([]Auth, error) { return f, nil }

func TestMultiSourceConcatenates(t *testing.T) {
	got, err := MultiSource(fixed{{Host: "a"}}, nil, fixed{{Host: "b"}}).Auths(context.Background())
	if err != nil || len(got) != 2 || got[0].Host != "a" || got[1].Host != "b" {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestSwitchUnsetIsEmpty(t *testing.T) {
	var s Switch
	if got, err := s.Auths(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("unset = %v, %v", got, err)
	}
	s.Set(fixed{{Host: "a"}})
	if got, _ := s.Auths(context.Background()); len(got) != 1 {
		t.Fatalf("set = %v", got)
	}
}

func TestStoreIsASource(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, External{Host: "ghcr.io", Username: "u", Secret: "s"}, t0); err != nil {
		t.Fatal(err)
	}
	var src Source = s
	got, err := src.Auths(ctx)
	if err != nil || !reflect.DeepEqual(got, []Auth{{Host: "ghcr.io", Username: "u", Secret: "s"}}) {
		t.Fatalf("got %v, %v", got, err)
	}
}
