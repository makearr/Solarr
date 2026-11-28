package settings

import (
	"context"
	"slices"
	"strconv"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// How big and how compressed stored pictures are made (Settings -> Database).
const (
	KeyCoverMaxEdge    = "images.cover_max_edge"
	KeyCoverQuality    = "images.cover_quality"
	KeyPreviewMaxEdge  = "images.preview_max_edge"
	KeyPreviewQuality  = "images.preview_quality"
	KeyImageFormat     = "images.format"
	DefaultCoverEdge   = 1000
	DefaultCoverWebP   = 82
	DefaultPreviewEdge = 1280
	DefaultPreviewJPEG = 80
	// DefaultImageFormat is the format of stored pictures and video previews until a person picks another.
	DefaultImageFormat = "webp"
)

// ImageFormats are the formats stored pictures and video previews can be written in.
var ImageFormats = []string{"webp", "jpg", "png"}

// ImageSettings are the longest edge and quality of the pictures of library items and of video previews, and the one
// format both are written in.
type ImageSettings struct {
	CoverMaxEdge, CoverQuality, PreviewMaxEdge, PreviewQuality int64
	Format                                                     string
}

// ImageSettings reads the configured figures.
func (s *Snapshot) ImageSettings() ImageSettings {
	return ImageSettings{
		CoverMaxEdge: s.policyInt(KeyCoverMaxEdge, DefaultCoverEdge), CoverQuality: s.policyInt(KeyCoverQuality, DefaultCoverWebP),
		PreviewMaxEdge: s.policyInt(KeyPreviewMaxEdge, DefaultPreviewEdge), PreviewQuality: s.policyInt(KeyPreviewQuality, DefaultPreviewJPEG),
		Format: s.imageFormat(),
	}
}

func (s *Snapshot) imageFormat() string {
	if format := s.policy[KeyImageFormat]; slices.Contains(ImageFormats, format) {
		return format
	}
	return DefaultImageFormat
}

func validateImageSettings(images ImageSettings) config.Problems {
	var problems config.Problems
	if !slices.Contains(ImageFormats, images.Format) {
		problems = append(problems, config.Problem{Path: KeyImageFormat, Value: images.Format, Expectation: "one of webp, jpg, png"})
	}
	for _, check := range []struct {
		key           string
		value, lo, hi int64
	}{
		{KeyCoverMaxEdge, images.CoverMaxEdge, 400, 4000}, {KeyCoverQuality, images.CoverQuality, 30, 95},
		{KeyPreviewMaxEdge, images.PreviewMaxEdge, 320, 4000}, {KeyPreviewQuality, images.PreviewQuality, 30, 95},
	} {
		if check.value < check.lo || check.value > check.hi {
			problems = append(problems, config.Problem{Path: check.key, Value: strconv.FormatInt(check.value, 10),
				Expectation: strconv.FormatInt(check.lo, 10) + " to " + strconv.FormatInt(check.hi, 10)})
		}
	}
	return problems
}

// SetImageSettings writes the figures (ADR-44: validated on write); it applies to pictures made from then on.
func (c *Cache) SetImageSettings(ctx context.Context, cl clock.Clock, images ImageSettings) error {
	if problems := validateImageSettings(images); len(problems) > 0 {
		return problems
	}
	return c.setPolicies(ctx, cl, map[string]string{
		KeyCoverMaxEdge: strconv.FormatInt(images.CoverMaxEdge, 10), KeyCoverQuality: strconv.FormatInt(images.CoverQuality, 10),
		KeyPreviewMaxEdge: strconv.FormatInt(images.PreviewMaxEdge, 10), KeyPreviewQuality: strconv.FormatInt(images.PreviewQuality, 10),
		KeyImageFormat: images.Format,
	})
}
