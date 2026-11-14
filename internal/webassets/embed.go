package webassets

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

//go:generate pnpm --dir ../../web build

// dist is build output and stays out of git (each worktree builds its own);
// only dist/.gitkeep is committed, and all: embeds it so a checkout that never
// built the frontend still compiles.
//
//go:embed all:dist
var files embed.FS

// Handler serves the compiled Vite application from the binary. The frontend
// routes with history URLs, so any path that is not a real file in the build
// output gets the shell (dist/index.html) and the app's router resolves it;
// the API namespace is excluded and keeps answering 404 (or, unauthenticated,
// the auth middleware's 401) instead of HTML. Hashed files under assets/ are
// cached for good, the shell is revalidated by ETag, and a build-time .br or
// .gz sibling is sent when the browser accepts it.
func Handler() http.Handler {
	dist, err := fs.Sub(files, "dist")
	if err != nil {
		panic("embedded web assets are missing: " + err.Error())
	}
	return handler(dist)
}

// notBuilt is the page a binary built without the frontend serves.
const notBuilt = "The web frontend was not built into this binary. Run `task build-web` (or `task build`) and rebuild.\n"

func handler(dist fs.FS) http.Handler {
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(notBuilt))
		})
	}
	assets, err := index(dist)
	if err != nil {
		panic("index embedded web assets: " + err.Error())
	}
	shell := assets["index.html"]
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		a, ok := assets[name]
		if !ok {
			a = shell
		}
		a.serve(w, r, dist)
	})
}

// immutable is the Cache-Control of a file under assets/: Vite puts a content
// hash in every name there, so a changed file is a new URL and the browser
// never needs to ask again. Everything else, the shell above all, is
// revalidated by ETag on every load so a Panel update shows at once.
const immutable = "public, max-age=31536000, immutable"

// fallbackTypes covers extensions Go's built-in table lacks; a minimal image
// has no /etc/mime.types to fill them in.
var fallbackTypes = map[string]string{
	".ttf":   "font/ttf",
	".woff":  "font/woff",
	".woff2": "font/woff2",
}

// encodings are the precompressed siblings the build writes (web/vite.config.ts),
// in order of preference.
var encodings = []struct{ name, suffix string }{
	{"br", ".br"},
	{"gzip", ".gz"},
}

// asset is one servable file: its content type, an ETag per representation,
// and which precompressed siblings exist. The panel runs on a quarter CPU, so
// nothing is compressed per request; the build does it once.
type asset struct {
	name        string
	contentType string
	etag        string // identity ETag, quoted; a variant appends -<encoding>
	variants    map[string]bool
}

// index hashes every file once at start and pairs it with its .br/.gz
// siblings. The siblings themselves are not servable by their own names.
func index(dist fs.FS) (map[string]asset, error) {
	assets := map[string]asset{}
	err := fs.WalkDir(dist, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		for _, e := range encodings {
			if strings.HasSuffix(name, e.suffix) {
				return nil
			}
		}
		f, err := dist.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		sum := sha256.New()
		if _, err := io.Copy(sum, f); err != nil {
			return err
		}
		ext := path.Ext(name)
		ctype := mime.TypeByExtension(ext)
		if ctype == "" {
			ctype = fallbackTypes[ext]
		}
		a := asset{
			name:        name,
			contentType: ctype,
			etag:        hex.EncodeToString(sum.Sum(nil)[:12]),
			variants:    map[string]bool{},
		}
		// A variant is only safe with a known type: the browser cannot sniff
		// compressed bytes.
		if ctype != "" {
			for _, e := range encodings {
				if _, err := fs.Stat(dist, name+e.suffix); err == nil {
					a.variants[e.name] = true
				}
			}
		}
		assets[name] = a
		return nil
	})
	return assets, err
}

func (a asset) serve(w http.ResponseWriter, r *http.Request, dist fs.FS) {
	h := w.Header()
	if strings.HasPrefix(a.name, "assets/") {
		h.Set("Cache-Control", immutable)
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	if a.contentType != "" {
		h.Set("Content-Type", a.contentType)
	}
	name, etag := a.name, a.etag
	if len(a.variants) > 0 {
		h.Add("Vary", "Accept-Encoding")
		for _, e := range encodings {
			if a.variants[e.name] && accepts(r.Header.Get("Accept-Encoding"), e.name) {
				name, etag = a.name+e.suffix, a.etag+"-"+e.name
				h.Set("Content-Encoding", e.name)
				break
			}
		}
	}
	h.Set("ETag", `"`+etag+`"`)
	f, err := dist.Open(name)
	if err != nil {
		http.Error(w, "asset unavailable", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	content, ok := f.(io.ReadSeeker)
	if !ok {
		b, err := io.ReadAll(f)
		if err != nil {
			http.Error(w, "asset unavailable", http.StatusInternalServerError)
			return
		}
		content = bytes.NewReader(b)
	}
	http.ServeContent(w, r, a.name, time.Time{}, content)
}

// accepts reports whether an Accept-Encoding header allows coding, honouring
// an explicit q=0. A "*" is not taken as consent: only named codings are sent.
func accepts(header, coding string) bool {
	for _, part := range strings.Split(header, ",") {
		token, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(token), coding) {
			continue
		}
		for _, p := range strings.Split(params, ";") {
			k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
			if strings.EqualFold(k, "q") {
				if q, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && q == 0 {
					return false
				}
			}
		}
		return true
	}
	return false
}
