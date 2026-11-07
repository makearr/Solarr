package settings

import (
	"context"
	"strconv"

	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// How big and how compressed stored pictures are made (Settings -> Database).
const (
	KeyCoverMaxEdge    = "images.cover_max_edge"
	KeyCoverQuality    = "images.cover_quality"
	KeyPreviewMaxEdge  = "images.preview_max_edge"
	KeyPreviewQuality  = "images.preview_quality"
	DefaultCoverEdge   = 1000
	DefaultCoverWebP   = 82
	DefaultPreviewEdge = 1280
	DefaultPreviewJPEG = 80
)

// ImageSettings are the longest edge and quality of the pictures of library items and of video previews.
type ImageSettings struct {
	CoverMaxEdge, CoverQuality, PreviewMaxEdge, PreviewQuality int64
}

// ImageSettings reads the configured figures.
func (s *Snapshot) ImageSettings() ImageSettings {
	return ImageSettings{
		CoverMaxEdge: s.policyInt(KeyCoverMaxEdge, DefaultCoverEdge), CoverQuality: s.policyInt(KeyCoverQuality, DefaultCoverWebP),
		PreviewMaxEdge: s.policyInt(KeyPreviewMaxEdge, DefaultPreviewEdge), PreviewQuality: s.policyInt(KeyPreviewQuality, DefaultPreviewJPEG),
	}
}

func validateImageSettings(images ImageSettings) config.Problems {
	var problems config.Problems
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
	values := map[string]int64{
		KeyCoverMaxEdge: images.CoverMaxEdge, KeyCoverQuality: images.CoverQuality,
		KeyPreviewMaxEdge: images.PreviewMaxEdge, KeyPreviewQuality: images.PreviewQuality,
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		for key, value := range values {
			if err := queries.SetInstanceSetting(ctx, settingsdb.SetInstanceSettingParams{
				Key: key, ValueJson: strconv.FormatInt(value, 10), UpdatedAt: cl.Now().Unix(),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}
