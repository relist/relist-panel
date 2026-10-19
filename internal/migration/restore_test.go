package migration

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"relist-panel/internal/volfs"
)

func newTestRestorer(t *testing.T) (*restorer, *FakeDocker, string) {
	t.Helper()
	docker := NewFakeDocker()
	staging := filepath.Join(t.TempDir(), "staging")
	return NewRestorer(LocalHost{Docker: docker}, staging).(*restorer), docker, staging
}

func savedBody(refs ...string) func(context.Context) (io.ReadCloser, error) {
	return func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("images:" + strings.Join(refs, ","))), nil
	}
}

func noFetch(t *testing.T) func(context.Context) (io.ReadCloser, error) {
	return func(context.Context) (io.ReadCloser, error) {
		t.Fatal("fetched the source's stream for an Image that could be pulled")
		return nil, nil
	}
}

func TestRestoreDatabaseAndDataDir(t *testing.T) {
	r, _, staging := newTestRestorer(t)
	ctx := context.Background()
	if err := r.Restore(ctx, Item{ID: "database", Kind: KindDatabase}, strings.NewReader("sqlite bytes")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(staging, "panel.db")); string(b) != "sqlite bytes" {
		t.Fatalf("staged db = %q", b)
	}
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, ".traefik"), 0o700)
	os.WriteFile(filepath.Join(src, ".traefik", "acme.json"), []byte("{}"), 0o600)
	var packed bytes.Buffer
	root, _ := volfs.Open(src)
	root.Pack(".", &packed)
	root.Close()
	if err := r.Restore(ctx, Item{ID: "data-dir", Kind: KindDataDir}, &packed); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(staging, "data", ".traefik", "acme.json")); string(b) != "{}" {
		t.Fatalf("staged acme.json = %q", b)
	}
	r.Cleanup(ctx, Item{ID: "data-dir", Kind: KindDataDir})
	if _, err := os.Stat(filepath.Join(staging, "data")); !os.IsNotExist(err) {
		t.Fatal("Cleanup left the staged data dir")
	}
}

func TestRestoreVolumeKeepsNameDriverLabels(t *testing.T) {
	r, docker, _ := newTestRestorer(t)
	v := VolumeInfo{Name: "relist-c1_pgdata", Driver: "local", Labels: map[string]string{"com.docker.compose.project": "relist-c1"}}
	if err := r.Restore(context.Background(), Item{ID: "volume-relist-c1_pgdata", Kind: KindVolume, Volume: &v}, strings.NewReader("tar")); err != nil {
		t.Fatal(err)
	}
	got := docker.Volumes["relist-c1_pgdata"]
	if got.Driver != "local" || got.Labels["com.docker.compose.project"] != "relist-c1" || string(docker.Unpacked["relist-c1_pgdata"]) != "tar" {
		t.Fatalf("volume = %+v, unpacked %q", got, docker.Unpacked["relist-c1_pgdata"])
	}
}

func TestRestoreHostPathFileAndDir(t *testing.T) {
	r, docker, _ := newTestRestorer(t)
	for _, p := range []string{"/srv/uploads", "/opt/app/config.yml"} {
		if err := r.Restore(context.Background(), Item{Kind: KindHostPath, HostPath: p}, strings.NewReader(p)); err != nil {
			t.Fatal(err)
		}
		if string(docker.Unpacked[filepath.Dir(p)]) != p {
			t.Fatalf("%s unpacked into %v", p, docker.Unpacked)
		}
	}
	if got := docker.CallsOf("unpack"); !slices.Equal(got, []string{"unpack /srv uploads", "unpack /opt/app config.yml"}) {
		t.Fatalf("unpacks = %q, want each confined to its own name", got)
	}
	r.Cleanup(context.Background(), Item{Kind: KindHostPath, HostPath: "/srv/uploads"})
	if got := docker.CallsOf("delete"); !slices.Equal(got, []string{"delete /srv uploads"}) {
		t.Fatalf("deletes = %q", got)
	}
}

func TestRestoreImagePullsByDigestAndTags(t *testing.T) {
	r, docker, _ := newTestRestorer(t)
	img := ImageInfo{ID: "sha256:pg", Tags: []string{"postgres:16"}, RepoDigests: []string{"postgres@sha256:d1"}}
	docker.Pullable["postgres@sha256:d1"] = ImageInfo{ID: "sha256:pg", RepoDigests: img.RepoDigests}
	warning, err := r.RestoreImage(context.Background(), Item{Kind: KindImage, Image: &img}, noFetch(t))
	if err != nil || warning != "" {
		t.Fatalf("RestoreImage() = %q, %v", warning, err)
	}
	if got := docker.Images["postgres:16"]; got.ID != "sha256:pg" {
		t.Fatalf("postgres:16 = %+v", got)
	}
}

func TestRestoreImagePullFailureFallsBackToLoad(t *testing.T) {
	r, docker, _ := newTestRestorer(t)
	img := ImageInfo{ID: "sha256:priv", Tags: []string{"registry.example.com/app:1"}, RepoDigests: []string{"registry.example.com/app@sha256:x"}}
	docker.Loadable["registry.example.com/app:1"] = img
	if _, err := r.RestoreImage(context.Background(), Item{Kind: KindImage, Image: &img}, savedBody(img.Tags...)); err != nil {
		t.Fatal(err)
	}
	if docker.Images["registry.example.com/app:1"].ID != "sha256:priv" {
		t.Fatal("image not loaded")
	}
}

func TestRestoreImageLoadKeepsTags(t *testing.T) {
	r, docker, _ := newTestRestorer(t)
	img := ImageInfo{ID: "sha256:built", Tags: []string{"relist-c7-api:latest"}, RepoDigests: []string{}}
	docker.Loadable["relist-c7-api:latest"] = img
	if _, err := r.RestoreImage(context.Background(), Item{Kind: KindImage, Image: &img}, savedBody(img.Tags...)); err != nil {
		t.Fatal(err)
	}
	if got := docker.Images["relist-c7-api:latest"]; got.ID != "sha256:built" {
		t.Fatalf("relist-c7-api:latest = %+v", got)
	}
	if len(docker.CallsOf("pull")) != 0 {
		t.Fatal("pulled an Image without RepoDigests")
	}
}

func TestRestoreUntaggedImageGetsMigratedTag(t *testing.T) {
	r, docker, _ := newTestRestorer(t)
	img := ImageInfo{ID: "sha256:0123456789abcdef", Tags: []string{}, RepoDigests: []string{}}
	docker.Loadable["sha256:0123456789abcdef"] = img
	if _, err := r.RestoreImage(context.Background(), Item{Kind: KindImage, Image: &img}, savedBody(img.ID)); err != nil {
		t.Fatal(err)
	}
	if got := docker.Images["relist-migrated/0123456789ab"]; got.ID != img.ID {
		t.Fatalf("migrated tag = %+v", got)
	}
}

func TestLoadedImageIDMismatchIsWarning(t *testing.T) {
	r, docker, _ := newTestRestorer(t)
	img := ImageInfo{ID: "sha256:classic", Tags: []string{"app:1"}, RepoDigests: []string{}}
	docker.Loadable["app:1"] = ImageInfo{ID: "sha256:containerd", Tags: []string{"app:1"}}
	warning, err := r.RestoreImage(context.Background(), Item{Kind: KindImage, Image: &img}, savedBody("app:1"))
	if err != nil {
		t.Fatal(err)
	}
	if warning != "Image app:1 has a different ID on this host (image store differs); content was loaded from the source" {
		t.Fatalf("warning = %q", warning)
	}
}

func TestPulledImageIDMismatchFallsBackToLoad(t *testing.T) {
	r, docker, _ := newTestRestorer(t)
	img := ImageInfo{ID: "sha256:want", Tags: []string{"app:1"}, RepoDigests: []string{"app@sha256:d"}}
	docker.Pullable["app@sha256:d"] = ImageInfo{ID: "sha256:other"}
	docker.Loadable["app:1"] = img
	fetched := false
	fetch := func(ctx context.Context) (io.ReadCloser, error) { fetched = true; return savedBody("app:1")(ctx) }
	if _, err := r.RestoreImage(context.Background(), Item{Kind: KindImage, Image: &img}, fetch); err != nil {
		t.Fatal(err)
	}
	if !fetched || docker.Images["app:1"].ID != "sha256:want" {
		t.Fatalf("fetched %v, app:1 = %+v", fetched, docker.Images["app:1"])
	}
}

func TestCleanupAndDiscard(t *testing.T) {
	r, docker, _ := newTestRestorer(t)
	ctx := context.Background()
	v := VolumeInfo{Name: "vol", Driver: "local"}
	r.Restore(ctx, Item{Kind: KindVolume, Volume: &v}, strings.NewReader("x"))
	if err := r.Cleanup(ctx, Item{Kind: KindVolume, Volume: &v}); err != nil {
		t.Fatal(err)
	}
	if _, ok := docker.Volumes["vol"]; ok {
		t.Fatal("Cleanup left the volume")
	}
	img := ImageInfo{ID: "sha256:a", Tags: []string{"a:1"}}
	docker.Loadable["a:1"] = img
	r.RestoreImage(ctx, Item{Kind: KindImage, Image: &img}, savedBody("a:1"))
	r.Restore(ctx, Item{Kind: KindVolume, Volume: &v}, strings.NewReader("x"))
	if err := r.Discard(ctx, []Item{{Kind: KindVolume, Volume: &v}, {Kind: KindImage, Image: &img}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := docker.Volumes["vol"]; ok {
		t.Fatal("Discard left the volume")
	}
	if _, ok := docker.Images["a:1"]; ok {
		t.Fatal("Discard left the image tag")
	}
}

func TestUntaggedImageWithAnotherIDIsTaggedByTheLoadedID(t *testing.T) {
	r, docker, _ := newTestRestorer(t)
	img := ImageInfo{ID: "sha256:0123456789abcdef", Tags: []string{}, RepoDigests: []string{}}
	docker.Loadable["sha256:0123456789abcdef"] = ImageInfo{ID: "sha256:fedcba9876543210"}
	warning, err := r.RestoreImage(context.Background(), Item{Kind: KindImage, Image: &img}, savedBody(img.ID))
	if err != nil {
		t.Fatalf("RestoreImage() error = %v, want a warning", err)
	}
	if got := docker.Images["relist-migrated/0123456789ab"]; got.ID != "sha256:fedcba9876543210" {
		t.Fatalf("migrated tag = %+v", got)
	}
	if !strings.Contains(warning, "different ID") {
		t.Fatalf("warning = %q", warning)
	}
}
