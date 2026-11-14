package webassets

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func cachingDist() fstest.MapFS {
	return fstest.MapFS{
		"index.html":              {Data: []byte("<!doctype html><div id=root></div>")},
		"assets/index-a.js":       {Data: []byte("console.log('identity')")},
		"assets/index-a.js.br":    {Data: []byte("BROTLI-BYTES")},
		"assets/index-a.js.gz":    {Data: []byte("GZIP-BYTES")},
		"assets/plain-b.css":      {Data: []byte("body{}")},
		"assets/codicon-c.ttf":    {Data: []byte("TTF")},
		"assets/codicon-c.ttf.gz": {Data: []byte("TTF-GZ")},
	}
}

func get(t *testing.T, h http.Handler, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHashedAssetsAreImmutable(t *testing.T) {
	h := handler(cachingDist())
	rec := get(t, h, "/assets/plain-b.css", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestShellIsRevalidatedWithETag(t *testing.T) {
	h := handler(cachingDist())
	for _, path := range []string{"/", "/index.html", "/composes/7"} {
		rec := get(t, h, path, nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<div id=root>") {
			t.Fatalf("GET %s = %d %q", path, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s Cache-Control = %q, want no-cache", path, got)
		}
		etag := rec.Header().Get("ETag")
		if etag == "" {
			t.Fatalf("GET %s has no ETag", path)
		}
		again := get(t, h, path, map[string]string{"If-None-Match": etag})
		if again.Code != http.StatusNotModified {
			t.Errorf("GET %s with If-None-Match = %d, want 304", path, again.Code)
		}
	}
}

func TestPrecompressedVariantFollowsAcceptEncoding(t *testing.T) {
	h := handler(cachingDist())
	cases := []struct {
		accept, wantEncoding, wantBody string
	}{
		{"gzip, deflate, br, zstd", "br", "BROTLI-BYTES"},
		{"gzip", "gzip", "GZIP-BYTES"},
		{"br;q=0, gzip", "gzip", "GZIP-BYTES"},
		{"", "", "console.log('identity')"},
		{"identity", "", "console.log('identity')"},
	}
	etags := map[string]string{}
	for _, c := range cases {
		rec := get(t, h, "/assets/index-a.js", map[string]string{"Accept-Encoding": c.accept})
		if rec.Code != http.StatusOK {
			t.Fatalf("Accept-Encoding %q: status %d", c.accept, rec.Code)
		}
		if got := rec.Header().Get("Content-Encoding"); got != c.wantEncoding {
			t.Errorf("Accept-Encoding %q: Content-Encoding = %q, want %q", c.accept, got, c.wantEncoding)
		}
		if rec.Body.String() != c.wantBody {
			t.Errorf("Accept-Encoding %q: body = %q, want %q", c.accept, rec.Body.String(), c.wantBody)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") {
			t.Errorf("Accept-Encoding %q: Content-Type = %q", c.accept, got)
		}
		if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
			t.Errorf("Accept-Encoding %q: Vary = %q", c.accept, got)
		}
		if prev, ok := etags[c.wantEncoding]; ok && prev != rec.Header().Get("ETag") {
			t.Errorf("same encoding, different ETags: %q vs %q", prev, rec.Header().Get("ETag"))
		}
		etags[c.wantEncoding] = rec.Header().Get("ETag")
	}
	if etags["br"] == etags["gzip"] || etags["gzip"] == etags[""] || etags["br"] == etags[""] {
		t.Errorf("each encoding needs its own ETag, got %v", etags)
	}
}

func TestPrecompressedFontKeepsItsContentType(t *testing.T) {
	h := handler(cachingDist())
	rec := get(t, h, "/assets/codicon-c.ttf", map[string]string{"Accept-Encoding": "gzip"})
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Body.String() != "TTF-GZ" {
		t.Fatalf("got encoding %q body %q", rec.Header().Get("Content-Encoding"), rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "font/ttf" {
		t.Errorf("Content-Type = %q, want font/ttf", got)
	}
}

func TestCompressedFilesAndDirectoriesAreNotServedDirectly(t *testing.T) {
	h := handler(cachingDist())
	for _, path := range []string{"/assets/index-a.js.br", "/assets/", "/assets"} {
		rec := get(t, h, path, nil)
		if !strings.Contains(rec.Body.String(), "<div id=root>") {
			t.Errorf("GET %s = %d %q, want the shell", path, rec.Code, rec.Body.String())
		}
	}
}

func TestAPIPathStays404(t *testing.T) {
	rec := get(t, handler(cachingDist()), "/api/nothing", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/nothing = %d, want 404", rec.Code)
	}
}
