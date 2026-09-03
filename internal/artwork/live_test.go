package artwork_test

import (
	"bytes"
	"io"
	"net/http"
	"testing"
	"time"

	xwebp "golang.org/x/image/webp"

	"github.com/makearr/MediaNexus/internal/artwork"
)

// coverArtURL is KI Pecezinu's real front cover, fetched live during design of
// this package -- the archive's own 1200px thumbnail rather than the 13MB
// original, so the test stays fast without giving up a real >1000px source.
// A bare http.Client is fine here (forbidigo exempts _test.go, section 11)
// since this is not a production outbound call.
const coverArtURL = "https://coverartarchive.org/release/d319c059-baaa-4fc8-8ae2-cc3035c5fd19/43287476628-1200.jpg"

// TestProcess_AgainstARealFetchedCoverImage runs the whole pipeline against a
// real JPEG/PNG fetched over the network rather than only a synthetic image.
// It skips, rather than fails, when the sandbox has no outbound network, so
// make test stays deterministic wherever it runs.
func TestProcess_AgainstARealFetchedCoverImage(t *testing.T) {
	client := &http.Client{Timeout: 20 * time.Second}
	response, err := client.Get(coverArtURL)
	if err != nil {
		t.Skipf("live coverartarchive.org unreachable, skipping: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Skipf("live coverartarchive.org returned %d, skipping", response.StatusCode)
	}

	source, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		t.Skipf("read live image failed, skipping: %v", err)
	}

	result, err := artwork.Process(source)
	if err != nil {
		t.Fatalf("Process(real cover art): %v", err)
	}

	for _, size := range []artwork.Size{artwork.Thumb, artwork.Small, artwork.Full} {
		out, ok := result[size]
		if !ok || len(out.Bytes) == 0 {
			t.Fatalf("size %d: missing or empty output", size)
		}
		if max(out.Width, out.Height) != int(size) {
			t.Errorf("size %d: longest edge = %dx%d, want %d (the 1200px thumbnail is over every target)",
				size, out.Width, out.Height, size)
		}
		decoded, err := xwebp.Decode(bytes.NewReader(out.Bytes))
		if err != nil {
			t.Fatalf("size %d: decode back failed, corrupt WebP: %v", size, err)
		}
		if bounds := decoded.Bounds(); bounds.Dx() != out.Width || bounds.Dy() != out.Height {
			t.Errorf("size %d: decoded %dx%d, want %dx%d", size, bounds.Dx(), bounds.Dy(), out.Width, out.Height)
		}
	}
}
