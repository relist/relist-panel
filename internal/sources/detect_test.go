package sources

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestDetectDockerfiles(t *testing.T) {
	src := t.TempDir()
	for _, f := range []string{
		"Dockerfile",
		"api/Dockerfile",
		"web/Dockerfile.prod",
		"tools/dev.Dockerfile",
		"node_modules/x/Dockerfile",
		"vendor/y/Dockerfile",
		".git/Dockerfile",
		"a/b/c/d/e/Dockerfile",
		"docs/Dockerfile.md.txt.x/readme",
	} {
		write(t, filepath.Join(src, f), "FROM scratch")
	}
	got, err := DetectDockerfiles(src, "My Shop")
	if err != nil {
		t.Fatal(err)
	}
	want := []Dockerfile{
		{Path: "Dockerfile", Service: "my-shop", Context: "./src", Dockerfile: "Dockerfile"},
		{Path: "api/Dockerfile", Service: "api", Context: "./src/api", Dockerfile: "Dockerfile"},
		{Path: "tools/dev.Dockerfile", Service: "dev", Context: "./src/tools", Dockerfile: "dev.Dockerfile"},
		{Path: "web/Dockerfile.prod", Service: "prod", Context: "./src/web", Dockerfile: "Dockerfile.prod"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestDetectDockerfilesDepthFourIncluded(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "a/b/c/d/Dockerfile"), "x")
	got, err := DetectDockerfiles(src, "x")
	if err != nil || len(got) != 1 || got[0].Context != "./src/a/b/c/d" {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestDetectDockerfilesDuplicateNames(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "one/api/Dockerfile"), "x")
	write(t, filepath.Join(src, "two/api/Dockerfile"), "x")
	write(t, filepath.Join(src, "three/api/Dockerfile"), "x")
	got, err := DetectDockerfiles(src, "x")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range got {
		names = append(names, d.Service)
	}
	want := []string{"api", "api-2", "api-3"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
}

func TestDetectDockerfilesMissingSrc(t *testing.T) {
	got, err := DetectDockerfiles(filepath.Join(t.TempDir(), "nope"), "x")
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v, %v", got, err)
	}
}
