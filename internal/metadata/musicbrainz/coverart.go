package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/makearr/Solarr/internal/platform/httpx"
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
// its own (section 21.6 fetches once on import; internal/platform/artwork owns the
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
	Approved   bool              `json:"approved"`
	Front      bool              `json:"front"`
	Image      string            `json:"image"`
	Thumbnails map[string]string `json:"thumbnails"`
	Types      []string          `json:"types"`
}

// coverArtThumbnails are the archive's ready-made renditions, largest first.
// 1200 is the smallest that still covers platform/artwork's largest size
// (1000); the original upload is often many megabytes behind a slow redirect,
// only to be shrunk anyway, which is why covers used to arrive at roughly one
// every nine seconds (2026-09-17).
var coverArtThumbnails = []string{"1200", "large", "500"}

// url is the rendition worth downloading: a large thumbnail, else the original.
func (i coverArtImage) url() string {
	for _, size := range coverArtThumbnails {
		if thumbnail := i.Thumbnails[size]; thumbnail != "" {
			return thumbnail
		}
	}
	return i.Image
}

// Fetch answers the best available source image URL for a release: the
// standard http.Client the guard's transport wraps follows the archive.org
// redirect chain itself, so this is a plain GET. ErrNoCoverArt distinguishes
// "this release has no art" from a real failure.
func (c CoverArt) Fetch(ctx context.Context, releaseMBID string) (string, error) {
	return c.fetch(ctx, "release", releaseMBID)
}

// FetchReleaseGroup is Fetch for a release group, whose art the archive
// takes from one of its releases. A wanted album with no edition yet has only
// this id, and a release with no art of its own may still have a sibling that does.
func (c CoverArt) FetchReleaseGroup(ctx context.Context, releaseGroupMBID string) (string, error) {
	return c.fetch(ctx, "release-group", releaseGroupMBID)
}

// CoverImage is one picture the archive holds for a release or release group.
type CoverImage struct {
	// URL is the rendition worth downloading; ThumbURL a small one to browse by.
	URL, ThumbURL string
	// Types are the archive's own labels: Front, Back, Booklet, Medium, ...
	Types []string
}

// List answers every picture the archive holds for a release (entity "release") or release group,
// for the user to pick from; none is not an error.
func (c CoverArt) List(ctx context.Context, entity, mbid string) ([]CoverImage, error) {
	parsed, err := c.get(ctx, entity, mbid)
	if errors.Is(err, ErrNoCoverArt) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	images := make([]CoverImage, 0, len(parsed.Images))
	for _, image := range parsed.Images {
		thumb := image.Thumbnails["250"]
		if thumb == "" {
			thumb = image.Thumbnails["small"]
		}
		if thumb == "" {
			thumb = image.url()
		}
		images = append(images, CoverImage{URL: image.url(), ThumbURL: thumb, Types: image.Types})
	}
	return images, nil
}

func (c CoverArt) fetch(ctx context.Context, entity, mbid string) (string, error) {
	parsed, err := c.get(ctx, entity, mbid)
	if err != nil {
		return "", err
	}
	image, ok := bestCoverArtImage(parsed.Images)
	if !ok {
		return "", ErrNoCoverArt
	}
	return image, nil
}

func (c CoverArt) get(ctx context.Context, entity, mbid string) (coverArtResponse, error) {
	url := fmt.Sprintf("%s/%s/%s", c.baseURL, entity, mbid)
	response, err := c.client.Get(ctx, url)
	if err != nil {
		var statusErr *httpx.StatusError
		if errors.As(err, &statusErr) && statusErr.Status == http.StatusNotFound {
			return coverArtResponse{}, ErrNoCoverArt
		}
		return coverArtResponse{}, fmt.Errorf("musicbrainz: cover art %s %s: %w", entity, mbid, err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return coverArtResponse{}, fmt.Errorf("musicbrainz: cover art %s %s: read response: %w", entity, mbid, err)
	}
	var parsed coverArtResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return coverArtResponse{}, fmt.Errorf("musicbrainz: cover art %s %s: decode response: %w", entity, mbid, err)
	}

	return parsed, nil
}

// bestCoverArtImage prefers a front, approved image (section 21.6's cover
// grid wants the one canonical image); an upload nobody has moderated yet is
// still better than nothing, so any image is the fallback.
func bestCoverArtImage(images []coverArtImage) (string, bool) {
	for _, image := range images {
		if image.Front && image.Approved {
			return image.url(), true
		}
	}
	if len(images) > 0 {
		return images[0].url(), true
	}
	return "", false
}
