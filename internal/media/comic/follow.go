package comic

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/comicvine"
)

const (
	// maxSeriesPerFollow bounds what one run adds for one follow; a back-catalogue follow asked for more.
	maxSeriesPerFollow        = 5
	maxSeriesPerBackCatalogue = 25
)

// CreatorSource lists the series a creator has credits on, found by name.
type CreatorSource interface {
	Configured() bool
	CreatorSeries(ctx context.Context, name string) ([]comicvine.Series, error)
}

// Following is the comic type's part of a follow: the series a followed creator has credits on at ComicVine, those that
// started from the year of the follow on, or every one for a back-catalogue follow.
type Following struct {
	Source  CreatorSource
	People  library.People
	Catalog Catalog
}

var _ media.Following = Following{}

// FollowLimit implements media.Following.
func (Following) FollowLimit(follow media.Follow) int {
	if follow.BackCatalogue {
		return maxSeriesPerBackCatalogue
	}
	return maxSeriesPerFollow
}

// FollowCandidates implements media.Following: newest series first, with what the library holds marked.
func (f Following) FollowCandidates(ctx context.Context, follow media.Follow) ([]media.FollowCandidate, error) {
	if follow.SubjectType != "person" || f.Source == nil || !f.Source.Configured() {
		return nil, media.ErrNoFollowSource
	}
	person, err := f.People.Person(ctx, follow.SubjectID)
	if err != nil {
		return nil, err
	}
	found, err := f.Source.CreatorSeries(ctx, person.Name)
	if err != nil {
		return nil, fmt.Errorf("comic: follow %s: %w", person.Name, err)
	}
	since := 0
	if !follow.BackCatalogue {
		since = time.Unix(follow.CreatedAt, 0).UTC().Year()
	}
	source := catalogSource{f.Catalog.Service}
	var candidates []media.FollowCandidate
	for _, series := range found {
		if since > 0 && series.StartYear < since {
			continue
		}
		id := fmt.Sprint(series.ID)
		_, held := source.Held(ctx, ProviderComicVine, id)
		candidates = append(candidates, media.FollowCandidate{
			Provider: ProviderComicVine, ExternalID: id, Title: series.Name, Year: int64(series.StartYear), PosterURL: series.CoverURL, Held: held,
		})
	}
	slices.SortStableFunc(candidates, func(a, b media.FollowCandidate) int { return int(b.Year - a.Year) })
	return candidates, nil
}

// AddFollowed implements media.Following: added as the add dialog adds it, monitored.
func (f Following) AddFollowed(ctx context.Context, follow media.Follow, candidate media.FollowCandidate) error {
	_, err := f.Catalog.AddWork(ctx, media.AddRequest{
		CategoryID: follow.CategoryID, Kind: AddKindSeries, Provider: candidate.Provider, ExternalID: candidate.ExternalID, Monitored: true,
	})
	return err
}
