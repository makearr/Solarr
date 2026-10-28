package book

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/metadata/openlibrary"
)

const (
	// maxBooksPerFollow bounds what one run adds for one follow, as Readarr's author refresh adds a few at a time; a
	// back-catalogue follow is allowed more because it asked for it.
	maxBooksPerFollow        = 5
	maxBooksPerBackCatalogue = 25
)

// Following is a book format's part of a follow, Readarr's author monitoring: the author's books at Open Library, those
// first published from the year of the follow on, or every one for a back-catalogue follow.
type Following struct {
	Authors AuthorWorks
	Catalog Catalog
}

// FollowLimit implements media.Following.
func (Following) FollowLimit(follow media.Follow) int {
	if follow.BackCatalogue {
		return maxBooksPerBackCatalogue
	}
	return maxBooksPerFollow
}

// FollowCandidates implements media.Following: only an author is followed, since Open Library lists books by author.
func (f Following) FollowCandidates(ctx context.Context, follow media.Follow) ([]media.FollowCandidate, error) {
	if follow.SubjectType != "person" || (len(follow.Roles) > 0 && !slices.Contains(follow.Roles, RoleAuthor)) || f.Authors.Source == nil {
		return nil, nil
	}
	person, err := f.Authors.People.Person(ctx, follow.SubjectID)
	if err != nil {
		return nil, err
	}
	docs, err := f.Authors.writtenBy(ctx, person.Name)
	if err != nil {
		return nil, fmt.Errorf("book: follow %s: %w", person.Name, err)
	}
	since := 0
	if !follow.BackCatalogue {
		since = time.Unix(follow.CreatedAt, 0).UTC().Year()
	}
	held := map[string]bool{}
	if titles, err := f.Authors.Reader.TitlesOf(ctx, f.Authors.Format.MediaType, follow.CategoryID); err == nil {
		for _, one := range titles {
			held[foldTitle(one.Title)] = true
		}
	}
	source := catalogSource{f.Catalog.Service}
	var candidates []media.FollowCandidate
	for _, doc := range docs {
		if since > 0 && doc.FirstPublishYear < since {
			continue
		}
		provider := f.Authors.Format.Provider(ProviderOpenLibrary)
		_, heldByID := source.Held(ctx, provider, doc.WorkID())
		candidates = append(candidates, media.FollowCandidate{
			Provider: provider, ExternalID: doc.WorkID(), Title: doc.Title, Year: int64(doc.FirstPublishYear),
			PosterURL: openlibrary.CoverURL(doc.CoverID), Role: RoleAuthor, Held: heldByID || held[foldTitle(doc.Title)],
		})
	}
	slices.SortStableFunc(candidates, func(a, b media.FollowCandidate) int { return int(b.Year - a.Year) })
	return candidates, nil
}

// AddFollowed implements media.Following: added as the add dialog adds it, monitored, but never past an exclusion.
func (f Following) AddFollowed(ctx context.Context, follow media.Follow, candidate media.FollowCandidate) error {
	catalog := works.Catalog{Source: catalogSource{f.Catalog.Service}, Categories: f.Catalog.Categories, DefaultProfileID: f.Catalog.DefaultProfileID}
	_, err := catalog.AddWork(ctx, media.AddRequest{
		CategoryID: follow.CategoryID, Kind: f.Authors.Format.MediaType, Provider: candidate.Provider, ExternalID: candidate.ExternalID, Monitored: true,
	})
	return err
}
