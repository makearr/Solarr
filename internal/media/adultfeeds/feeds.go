// Package adultfeeds reads a stash-box server's trending and upcoming scenes as the recommendation feeds of adult libraries.
package adultfeeds

import (
	"context"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/stashbox"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Charts is the part of the stash-box client the feeds read.
type Charts interface {
	Configured() bool
	Trending(ctx context.Context, page int) ([]stashbox.Scene, error)
	ReleasedAfter(ctx context.Context, day string, page int) ([]stashbox.Scene, error)
}

// Why the lists a stash-box server cannot serve are unavailable.
const (
	NoKeyNote   = "needs the server's API key in Settings"
	NoRatesNote = "stash-box servers keep no ratings"
)

// chartDepth makes a chart's own order the popularity: earlier pages rank above later ones.
const chartDepth = 100000

// Feeds recommends scenes from one stash-box server.
type Feeds struct {
	Charts Charts
	// Kind is the add kind of adult libraries, Provider the server's key a scene is added by, Source its display name.
	Kind, Provider, Source string
	Clock                  clock.Clock
}

var _ media.Recommending = Feeds{}

// RecommendationFeeds implements media.Recommending; the server answers only with its API key.
func (f Feeds) RecommendationFeeds(context.Context) ([]media.RecommendationFeed, map[media.RecommendationFeed]string) {
	feed := func(listType media.RecommendationType) media.RecommendationFeed {
		return media.RecommendationFeed{Type: listType, Kind: f.Kind, Source: f.Source}
	}
	unavailable := map[media.RecommendationFeed]string{
		feed(media.RecommendHighestRated): NoRatesNote, feed(media.RecommendClassics): NoRatesNote,
	}
	if !f.Charts.Configured() {
		unavailable[feed(media.RecommendTrending)], unavailable[feed(media.RecommendUpcoming)] = NoKeyNote, NoKeyNote
		return nil, unavailable
	}
	return []media.RecommendationFeed{feed(media.RecommendTrending), feed(media.RecommendUpcoming)}, unavailable
}

// Recommend implements media.Recommending.
func (f Feeds) Recommend(ctx context.Context, query media.RecommendQuery) ([]media.Recommendation, bool, error) {
	var (
		scenes []stashbox.Scene
		err    error
	)
	switch query.Feed.Type {
	case media.RecommendTrending:
		scenes, err = f.Charts.Trending(ctx, query.Page)
	case media.RecommendUpcoming:
		scenes, err = f.Charts.ReleasedAfter(ctx, f.Clock.Now().Format("2006-01-02"), query.Page)
	default:
		return nil, false, fmt.Errorf("%s: no feed %q: %w", f.Source, query.Feed.Type, media.ErrNotWired)
	}
	if err != nil {
		return nil, false, fmt.Errorf("%s %s: %w", f.Source, query.Feed.Type, err)
	}
	items := make([]media.Recommendation, 0, len(scenes))
	for index, scene := range scenes {
		if scene.ID == "" || scene.Title == "" {
			continue
		}
		item := media.Recommendation{
			Kind: f.Kind, Provider: f.Provider, ExternalID: scene.ID, Title: scene.Title, Overview: scene.Details,
			PosterURL: widest(scene.Images), ReleasedOn: scene.ReleaseDate, Popularity: float64(chartDepth - (query.Page-1)*stashbox.ChartPageSize - index),
		}
		if len(scene.ReleaseDate) >= 4 {
			_, _ = fmt.Sscanf(scene.ReleaseDate[:4], "%d", &item.Year)
		}
		if scene.Studio != nil {
			item.Subtitle, item.Networks = scene.Studio.Name, []string{scene.Studio.Name}
		}
		names := make([]string, 0, len(scene.Performers))
		for _, appearance := range scene.Performers {
			names = append(names, appearance.Performer.Name)
		}
		if len(names) > 0 {
			item.Subtitle = strings.TrimPrefix(item.Subtitle+" - "+strings.Join(names[:min(3, len(names))], ", "), " - ")
		}
		for _, tag := range scene.Tags {
			item.Traits.Genres = append(item.Traits.Genres, tag.Name)
		}
		items = append(items, item)
	}
	return items, len(scenes) == stashbox.ChartPageSize, nil
}

// widest is the address of the largest picture, empty when there is none.
func widest(images []stashbox.Image) string {
	best, size := "", -1
	for _, image := range images {
		if image.Width*image.Height > size {
			best, size = image.URL, image.Width*image.Height
		}
	}
	return best
}
