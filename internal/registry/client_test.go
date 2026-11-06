package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	indexDigest  = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	attestDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	amdDigest    = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	amdConfig    = "sha256:4444444444444444444444444444444444444444444444444444444444444444"
	plainDigest  = "sha256:5555555555555555555555555555555555555555555555555555555555555555"
	plainConfig  = "sha256:6666666666666666666666666666666666666666666666666666666666666666"
	attestConfig = "sha256:7777777777777777777777777777777777777777777777777777777777777777"
)

// fakeRegistry serves app:1 as a buildx OCI index (attestation first), app:2
// as a plain Docker v2 manifest, and records deletes.
func fakeRegistry(t *testing.T) (*Client, *[]string) {
	t.Helper()
	var deleted []string
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if u, p, ok := r.BasicAuth(); !ok || u != SystemUser || p != "sys" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /v2/_catalog", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("last") == "" {
			w.Header().Set("Link", `</v2/_catalog?last=app&n=1000>; rel="next"`)
			_, _ = w.Write([]byte(`{"repositories":["app"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"repositories":["team/web"]}`))
	}))
	mux.HandleFunc("GET /v2/team/web/tags/list", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"team/web","tags":null}`))
	}))
	mux.HandleFunc("GET /v2/app/tags/list", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"app","tags":["1","2"]}`))
	}))
	manifests := map[string]struct{ mediaType, digest, body string }{
		"1": {"application/vnd.oci.image.index.v1+json", indexDigest, `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[
			{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"` + attestDigest + `","size":500,"platform":{"architecture":"unknown","os":"unknown"}},
			{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"` + amdDigest + `","size":600,"platform":{"architecture":"amd64","os":"linux"}}]}`},
		attestDigest: {"application/vnd.oci.image.manifest.v1+json", attestDigest, `{"schemaVersion":2,"config":{"digest":"` + attestConfig + `","size":1},"layers":[{"size":99999}]}`},
		amdDigest:    {"application/vnd.oci.image.manifest.v1+json", amdDigest, `{"schemaVersion":2,"config":{"digest":"` + amdConfig + `","size":100},"layers":[{"size":1000},{"size":2000}]}`},
		"2":          {"application/vnd.docker.distribution.manifest.v2+json", plainDigest, `{"schemaVersion":2,"config":{"digest":"` + plainConfig + `","size":50},"layers":[{"size":700}]}`},
	}
	mux.HandleFunc("/v2/app/manifests/{ref}", auth(func(w http.ResponseWriter, r *http.Request) {
		ref := r.PathValue("ref")
		if r.Method == http.MethodDelete {
			deleted = append(deleted, ref)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		m, ok := manifests[ref]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if !strings.Contains(r.Header.Get("Accept"), "application/vnd.oci.image.index.v1+json") {
			http.Error(w, "accept lacks the OCI index", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", m.mediaType)
		w.Header().Set("Docker-Content-Digest", m.digest)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(m.body))
		}
	}))
	mux.HandleFunc("GET /v2/app/blobs/{digest}", auth(func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("digest") {
		case amdConfig:
			_, _ = w.Write([]byte(`{"created":"2026-11-05T10:00:00Z","architecture":"amd64"}`))
		case plainConfig:
			_, _ = w.Write([]byte(`{"created":"2026-11-04T10:00:00.5Z"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &Client{Base: server.URL, User: SystemUser, Secret: "sys", HTTP: server.Client()}, &deleted
}

func TestTagInfoReadsIndexSkippingAttestation(t *testing.T) {
	c, _ := fakeRegistry(t)
	info, err := c.TagInfo(context.Background(), "app", "1")
	if err != nil {
		t.Fatal(err)
	}
	want := TagInfo{Tag: "1", Digest: indexDigest, Size: 100 + 1000 + 2000, Created: time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)}
	if info.Tag != want.Tag || info.Digest != want.Digest || info.Size != want.Size || !info.Created.Equal(want.Created) {
		t.Fatalf("info = %+v, want %+v", info, want)
	}
}

func TestTagInfoPlainManifest(t *testing.T) {
	c, _ := fakeRegistry(t)
	info, err := c.TagInfo(context.Background(), "app", "2")
	if err != nil || info.Digest != plainDigest || info.Size != 750 || !info.Created.Equal(time.Date(2026, 11, 4, 10, 0, 0, 500_000_000, time.UTC)) {
		t.Fatalf("info = %+v, %v", info, err)
	}
}

func TestRepositoriesFollowsLink(t *testing.T) {
	c, _ := fakeRegistry(t)
	repos, err := c.Repositories(context.Background())
	if err != nil || !slices.Equal(repos, []string{"app", "team/web"}) {
		t.Fatalf("repos = %v, %v", repos, err)
	}
	tags, err := c.Tags(context.Background(), "team/web")
	if err != nil || tags == nil || len(tags) != 0 {
		t.Fatalf("null tags = %#v, %v", tags, err)
	}
	if tags, _ := c.Tags(context.Background(), "app"); !slices.Equal(tags, []string{"1", "2"}) {
		t.Fatalf("tags = %v", tags)
	}
}

func TestDeleteSendsDigest(t *testing.T) {
	c, deleted := fakeRegistry(t)
	if err := c.Delete(context.Background(), "app", indexDigest); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*deleted, []string{indexDigest}) {
		t.Fatalf("deleted = %v", *deleted)
	}
}

func TestClientWrongLoginErrors(t *testing.T) {
	c, _ := fakeRegistry(t)
	c.Secret = "wrong"
	if _, err := c.Repositories(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want a 401", err)
	}
}
