package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/makearr/MediaNexus/internal/httpx"
)

// CoverArtName is the ADR-13 rate-limit and breaker bucket for the Cover Art
// Archive -- a separate host from musicbrainz.org, so it earns its own bucket
// rather than sharing MusicBrainz's 1 req/s.
const CoverArtName = "coverartarchive"

// ErrNoCoverArt reports a release the Cover Art Archive has nothing for --
// section 21.6 treats a missing image as a normal placeholder case, not a
// failure worth logging loudly.
var ErrNoCoverArt = errors.New("musicbrainz: no cover art for release")

// CoverArt talks to coverartarchive.org: one call per release, no caching of
// its own (section 21.6 fetches once on import; internal/artwork owns the
// resized bytes this package has no business producing).
type CoverArt struct {
	client  *httpx.Client
	baseURL string
}

// NewCoverArt builds the Cover Art Archive client. client's Provider must be
// CoverArtName.
func NewCoverArt(client *httpx.Client, baseURL string) CoverArt {
	return CoverArt{client: client, baseURL: baseURL}
}

// coverArtResponse is the Cover Art Archive's own JSON shape -- an entirely
// different API from musicbrainz.org/ws/2, so it gets its own types here
// rather than sharing types.go.
type coverArtResponse struct {
	Images []coverArtImage `json:"images"`
}

type coverArtImage struct {
	Approved bool   `json:"approved"`
	Front    bool   `json:"front"`
	Image    string `json:"image"`
}

// Fetch answers the best available source image URL for a release: the
// standard http.Client the guard's transport wraps follows the archive.org
// redirect chain itself, so this is a plain GET. ErrNoCoverArt distinguishes
// "this release has no art" from a real failure.
func (c CoverArt) Fetch(ctx context.Context, releaseMBID string) (string, error) {
	url := fmt.Sprintf("%s/release/%s", c.baseURL, releaseMBID)
	response, err := c.client.Get(ctx, url)
	if err != nil {
		var statusErr *httpx.StatusError
		if errors.As(err, &statusErr) && statusErr.Status == http.StatusNotFound {
			return "", ErrNoCoverArt
		}
		return "", fmt.Errorf("musicbrainz: cover art %s: %w", releaseMBID, err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("musicbrainz: cover art %s: read response: %w", releaseMBID, err)
	}
	var parsed coverArtResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("musicbrainz: cover art %s: decode response: %w", releaseMBID, err)
	}

	image, ok := bestCoverArtImage(parsed.Images)
	if !ok {
		return "", ErrNoCoverArt
	}
	return image, nil
}

// bestCoverArtImage prefers a front, approved image (section 21.6's cover
// grid wants the one canonical image); an upload nobody has moderated yet is
// still better than nothing, so any image is the fallback.
func bestCoverArtImage(images []coverArtImage) (string, bool) {
	for _, image := range images {
		if image.Front && image.Approved {
			return image.Image, true
		}
	}
	if len(images) > 0 {
		return images[0].Image, true
	}
	return "", false
}
