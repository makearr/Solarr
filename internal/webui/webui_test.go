package webui_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/makearr/MediaNexus/internal/webui"
)

// Present in a release build, absent in a fresh checkout; both are defined.
func TestServesTheBundleOrExplainsItsAbsence(t *testing.T) {
	if webui.Built() {
		t.Run("built", assertServesBundle)
		return
	}
	t.Run("not built", assertExplainsAbsence)
}

func assertServesBundle(t *testing.T) {
	handler := webui.Handler()

	t.Run("root serves the document", func(t *testing.T) {
		response := get(handler, "/")
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", response.Code)
		}
		if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Errorf("Content-Type = %q, want text/html", got)
		}
		// A cached copy points at hashed assets that no longer exist.
		if got := response.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("Cache-Control = %q, want no-cache", got)
		}
	})

	t.Run("an unknown path is a client-side route, not a 404", func(t *testing.T) {
		response := get(handler, "/library/music/albums")
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 -- a reload on a client route must work", response.Code)
		}
		if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Errorf("Content-Type = %q, want the application document", got)
		}
	})

	t.Run("fingerprinted assets are cached immutably", func(t *testing.T) {
		asset := firstAssetPath(t, get(handler, "/").Body.String())

		response := get(handler, asset)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d for %s, want 200", response.Code, asset)
		}
		if got := response.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
			t.Errorf("Cache-Control = %q, want immutable -- the filename carries a hash, "+
				"so the contents can never change", got)
		}
	})
}

func assertExplainsAbsence(t *testing.T) {
	response := get(webui.Handler(), "/")

	// 503, not 404: the service runs, the interface is simply not in this build.
	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", response.Code)
	}
	if !strings.Contains(response.Body.String(), "make web") {
		t.Error("the page should name the command that fixes it")
	}
}

func get(handler http.Handler, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

func firstAssetPath(t *testing.T, document string) string {
	t.Helper()

	const marker = `"/assets/`
	start := strings.Index(document, marker)
	if start < 0 {
		t.Fatalf("the document references no /assets/ file:\n%s", document)
	}
	start += len(`"`)
	end := strings.IndexByte(document[start:], '"')
	if end < 0 {
		t.Fatal("unterminated asset reference in the document")
	}
	return document[start : start+end]
}
