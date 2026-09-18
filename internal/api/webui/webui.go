// Package webui serves the compiled web interface from inside the binary.
// See documentation/web-interface.md.
package webui

import (
	"bytes"
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// `all:` matches the committed .gitkeep, so a clean checkout builds.
//
//go:embed all:dist
var dist embed.FS

// indexPath is the document every client-side route resolves to.
const indexPath = "dist/index.html"

// Built reports whether a real bundle is embedded, rather than only the
// placeholder that keeps the embed pattern satisfied.
func Built() bool {
	_, err := fs.Stat(dist, indexPath)
	return err == nil
}

// Handler serves the bundle, falling back to index.html. Must be mounted after
// the API: it answers everything. See documentation/web-interface.md.
func Handler() http.Handler {
	assets, err := fs.Sub(dist, "dist")
	if err != nil {
		// Only reachable if the embed directive stopped matching.
		panic("webui: embedded bundle is not a directory: " + err.Error())
	}

	if !Built() {
		return http.HandlerFunc(explainMissingBundle)
	}

	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")

		if name == "" || name == "." {
			serveIndex(w, r, assets)
			return
		}

		file, err := assets.Open(name)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// A client-side route, not a missing file.
				serveIndex(w, r, assets)
				return
			}
			http.Error(w, "could not read the interface", http.StatusInternalServerError)
			return
		}
		_ = file.Close()

		// Fingerprinted, so the contents at a given URL can never change.
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, assets fs.FS) {
	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		http.Error(w, "the interface is not available", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// A stale copy points at hashed assets that no longer exist.
	w.Header().Set("Cache-Control", "no-cache")
	// Zero time skips If-Modified-Since: an embedded file has no useful one.
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(document))
}

// explainMissingBundle answers when the binary was built without the interface,
// since a 404 reads as a broken install rather than a missing build step.
func explainMissingBundle(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`<!doctype html>
<meta charset="utf-8">
<title>Solarr</title>
<body style="font-family: system-ui, sans-serif; max-width: 34rem; margin: 4rem auto; padding: 0 1rem">
<h1 style="font-size: 1.1rem">The web interface is not in this binary</h1>
<p>The API is running normally; only the compiled interface is absent. This is a
build that was made without it.</p>
<p>To build it: <code>make web</code>, then rebuild the binary.
For development, <code>make web-dev</code> serves it on :5173 and proxies here.</p>
</body>`))
}
