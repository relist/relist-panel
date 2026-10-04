package webassets

import (
	"embed"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
)

//go:generate pnpm --dir ../../web build

//go:embed dist
var files embed.FS

// Handler serves the compiled Vite application from the binary. The frontend
// routes with history URLs, so any path that is not a real file in the build
// output gets the shell (dist/index.html) and the app's router resolves it;
// the API namespace is excluded and keeps answering 404 (or, unauthenticated,
// the auth middleware's 401) instead of HTML.
func Handler() http.Handler {
	dist, err := fs.Sub(files, "dist")
	if err != nil {
		panic("embedded web assets are missing: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			name := strings.TrimPrefix(r.URL.Path, "/")
			if name == "" {
				name = "index.html"
			}
			if _, err := fs.Stat(dist, name); err != nil {
				if serveShell(w, dist) {
					return
				}
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}

// serveShell writes dist/index.html, reporting whether it could.
func serveShell(w http.ResponseWriter, dist fs.FS) bool {
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return false
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(index)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(index)
	return true
}
