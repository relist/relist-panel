package webassets

import (
	"embed"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
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
// the auth middleware's 401) instead of HTML.
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
