package webassets

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:generate pnpm --dir ../../web build

//go:embed dist
var files embed.FS

// Handler serves the compiled Vite application from the binary.
func Handler() http.Handler {
	dist, err := fs.Sub(files, "dist")
	if err != nil {
		panic("embedded web assets are missing: " + err.Error())
	}
	return http.FileServer(http.FS(dist))
}
