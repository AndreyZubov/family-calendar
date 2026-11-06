// Package web serves the built React app (frontend/dist), embedded into the
// server binary at build time. In production one process serves both the API
// and the UI from the same origin.
//
// The Docker build (and `make build`) copies frontend/dist into
// internal/web/dist before compiling. Without it (plain `go build` during
// development, where Vite serves the UI) the server answers UI requests with
// a short explanatory 404.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// Dist returns the embedded build output.
func Dist() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err) // the directive above guarantees "dist" exists
	}
	return sub
}

// Handler serves a single-page app from fsys:
//
//   - existing files are served as they are;
//   - other paths without a file extension get index.html, so client-side
//     routes like /family or /events/123 work on reload and from bookmarks;
//   - missing files with an extension (/assets/old.js) are a real 404 —
//     answering with HTML would make the browser fail with confusing MIME
//     errors after a deploy;
//   - /api/ and /auth/ never fall back to the app.
//
// Caching: Vite puts a content hash in every file name under /assets/, so
// those can be cached forever; index.html must always be revalidated, or
// users would keep loading the previous release's asset names.
func Handler(fsys fs.FS) http.Handler {
	_, err := fs.Stat(fsys, "index.html")
	built := err == nil

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/auth/") {
			http.NotFound(w, r)
			return
		}
		if !built {
			http.Error(w, "frontend not built into this binary: use the Vite dev server (make dev) or `make build`", http.StatusNotFound)
			return
		}

		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if info, err := fs.Stat(fsys, name); err == nil && !info.IsDir() {
			switch {
			case name == "index.html":
				w.Header().Set("Cache-Control", "no-cache")
			case strings.HasPrefix(name, "assets/"):
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			default:
				w.Header().Set("Cache-Control", "public, max-age=3600") // favicon etc.
			}
			// name is path.Clean'ed, fsys is the embedded build (nothing
			// outside it is reachable), and ServeFileFS rejects ".." itself.
			http.ServeFileFS(w, r, fsys, name) //nolint:gosec // G703: see above; covered by TestHandler/path_traversal
			return
		}
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		// Client-side route: let React Router handle it.
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, fsys, "index.html")
	})
}
