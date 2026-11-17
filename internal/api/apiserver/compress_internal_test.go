package apiserver

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveThroughCompress(t *testing.T, contentType, body, acceptEncoding string) *httptest.ResponseRecorder {
	t.Helper()
	handler := (&Server{}).compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, body)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/works", nil)
	if acceptEncoding != "" {
		request.Header.Set("Accept-Encoding", acceptEncoding)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestCompress(t *testing.T) {
	large := `{"items":"` + strings.Repeat("x", 2000) + `"}`
	small := `{"items":[]}`

	cases := []struct {
		name, contentType, body, accept string
		wantEncoding                    string
	}{
		{"a large JSON answer asked with gzip", "application/json", large, "br, gzip;q=0.8", "gzip"},
		{"a large problem document", "application/problem+json", large, "gzip", "gzip"},
		{"a small JSON answer", "application/json", small, "gzip", ""},
		{"no Accept-Encoding", "application/json", large, "", ""},
		{"an event stream", "text/event-stream", large, "gzip", ""},
		{"an image", "image/webp", large, "gzip", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := serveThroughCompress(t, tc.contentType, tc.body, tc.accept)
			if got := recorder.Header().Get("Content-Encoding"); got != tc.wantEncoding {
				t.Fatalf("Content-Encoding = %q, want %q", got, tc.wantEncoding)
			}
			if tc.wantEncoding == "" {
				if recorder.Body.String() != tc.body {
					t.Fatal("body changed without an encoding")
				}
				return
			}
			reader, err := gzip.NewReader(recorder.Body)
			if err != nil {
				t.Fatalf("gzip reader: %v", err)
			}
			plain, _ := io.ReadAll(reader)
			if string(plain) != tc.body {
				t.Fatal("decompressed body differs")
			}
		})
	}
}

func TestImageCacheControl(t *testing.T) {
	versioned := httptest.NewRequest(http.MethodGet, "/api/v1/works/1/artwork?size=small&v=3", nil)
	plain := httptest.NewRequest(http.MethodGet, "/api/v1/works/1/artwork?size=small", nil)
	if got := imageCacheControl(versioned, "private, max-age=3600"); got != "private, max-age=31536000, immutable" {
		t.Errorf("versioned = %q", got)
	}
	if got := imageCacheControl(plain, "private, max-age=3600"); got != "private, max-age=3600" {
		t.Errorf("unversioned = %q", got)
	}
}

// middleware between the stream and the connection (statusRecorder) only expose Unwrap; the flush must still get through.
func TestCompressPassesAFlushThroughAnUnwrappingWriter(t *testing.T) {
	recorder := httptest.NewRecorder()
	handler := (&Server{}).compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("Flush: %v", err)
		}
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(&statusRecorder{ResponseWriter: recorder, status: http.StatusOK}, request)
	if !recorder.Flushed {
		t.Fatal("the flush never reached the connection")
	}
}
