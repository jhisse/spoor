package web

import (
	"embed"
	"io/fs"
	"net/http"
)

// Vendored static assets, no npm, bundler or CDN: spoor.css (hand-written,
// no build step) and htmx.min.js (htmx 2.0.10).
//
//go:embed static
var staticFS embed.FS

// StaticHandler serves the embedded assets under /static/. embed.FS is not
// the OS filesystem: there is no path to traverse out of.
func StaticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // static is embedded: a missing dir is a build bug
	}
	return http.StripPrefix("/static/", http.FileServerFS(sub))
}
