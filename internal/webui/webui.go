// Package webui serves the compiled web interface from inside the binary.
//
// The bundle is written to dist/ by `make web` and embedded here, so a release
// is one static file with no assets to deploy beside it (ADR-1, ADR-19).
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

// dist holds the compiled bundle.
//
// The `all:` prefix is required: the directory carries a committed .gitkeep so
// this pattern always matches something, and a plain `embed dist` would skip a
// dotfile and fail to build on a clean checkout where nobody has run `make web`.
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

// Handler serves the bundle, falling back to index.html so that client-side
// routes survive a reload or a shared link.
//
// It must be mounted so the API is matched first: this handler answers
// *everything* it is given, because it cannot tell a client-side route from a
// typo, and guessing would turn a mistyped API path into a page of HTML.
func Handler() http.Handler {
	assets, err := fs.Sub(dist, "dist")
	if err != nil {
		// Only reachable if the embed directive above stopped matching, which
		// is a build-time mistake rather than a runtime condition.
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

		// Vite fingerprints asset filenames, so a given URL's contents can never
		// change. Anything else -- index.html above all -- must be revalidated,
		// or a deploy leaves browsers on the previous build indefinitely.
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
	// The document names the current hashed bundle, so a stale copy points at
	// assets that no longer exist and the page loads blank.
	w.Header().Set("Cache-Control", "no-cache")
	// A zero modification time tells ServeContent to skip If-Modified-Since:
	// an embedded file has no meaningful timestamp, and inventing one would
	// make caches trust a date that says nothing about the build.
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(document))
}

// explainMissingBundle answers when the binary was built without the interface.
//
// The alternative is a 404, which reads as a broken install rather than a build
// step nobody ran.
func explainMissingBundle(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`<!doctype html>
<meta charset="utf-8">
<title>MediaNexus</title>
<body style="font-family: system-ui, sans-serif; max-width: 34rem; margin: 4rem auto; padding: 0 1rem">
<h1 style="font-size: 1.1rem">The web interface is not in this binary</h1>
<p>The API is running normally; only the compiled interface is absent. This is a
build that was made without it.</p>
<p>To build it: <code>make web</code>, then rebuild the binary.
For development, <code>make web-dev</code> serves it on :5173 and proxies here.</p>
</body>`))
}
