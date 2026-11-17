package apiserver

import (
	"compress/gzip"
	"net/http"
	"strings"
)

// compressMinBytes is the first-write size under which gzip costs more than it saves.
const compressMinBytes = 1024

// compress gzips JSON answers whose first write is large; streams, images and already-encoded answers pass untouched.
func (s *Server) compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		writer := &gzipWriter{ResponseWriter: w}
		defer writer.finish()
		next.ServeHTTP(writer, r)
	})
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		if name, _, _ := strings.Cut(strings.TrimSpace(part), ";"); name == "gzip" {
			return true
		}
	}
	return false
}

type gzipWriter struct {
	http.ResponseWriter
	gzip    *gzip.Writer
	decided bool
	// status is held back until the first write, because the encoding headers must precede it.
	status int
}

func (g *gzipWriter) WriteHeader(status int) {
	if g.decided {
		g.ResponseWriter.WriteHeader(status)
		return
	}
	g.status = status
}

func (g *gzipWriter) sendHeader() {
	if g.status != 0 {
		g.ResponseWriter.WriteHeader(g.status)
		g.status = 0
	}
}

func (g *gzipWriter) Write(data []byte) (int, error) {
	if !g.decided {
		g.decided = true
		header := g.Header()
		isJSON := strings.Contains(header.Get("Content-Type"), "json")
		if isJSON && header.Get("Content-Encoding") == "" && len(data) >= compressMinBytes {
			header.Del("Content-Length")
			header.Set("Content-Encoding", "gzip")
			header.Add("Vary", "Accept-Encoding")
			g.gzip = gzip.NewWriter(g.ResponseWriter)
		}
		g.sendHeader()
	}
	if g.gzip != nil {
		return g.gzip.Write(data)
	}
	return g.ResponseWriter.Write(data)
}

// Flush pushes buffered compressed bytes first, so a flushed JSON stream still arrives.
func (g *gzipWriter) FlushError() error {
	g.decided = true
	g.sendHeader()
	if g.gzip != nil {
		if err := g.gzip.Flush(); err != nil {
			return err
		}
	}
	return http.NewResponseController(g.ResponseWriter).Flush()
}

func (g *gzipWriter) finish() {
	g.decided = true
	g.sendHeader()
	if g.gzip != nil {
		_ = g.gzip.Close()
	}
}

// Unwrap exposes the writer underneath, so http.ResponseController can reach SetWriteDeadline through this wrapper.
func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }
