package book

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

// SeriesSource answers every book of each series one of the Open Library works is part of.
type SeriesSource interface {
	BookSeries(ctx context.Context, openLibraryIDs []string) ([]wikidata.SeriesBook, error)
}

// SeriesBooks is a book format's part of a series collection: the series' books Wikidata lists that the library lacks,
// and monitoring the series, which adds them.
type SeriesBooks struct {
	Franchises library.Franchises
	Reader     library.SeriesReader
	Source     SeriesSource
	Catalog    Catalog
}

var _ media.Franchising = SeriesBooks{}

// MissingFranchiseMembers implements media.Franchising: the books of the collection's series, in series order, that the
// library holds under none of their Open Library ids and a person did not exclude.
func (s SeriesBooks) MissingFranchiseMembers(ctx context.Context, _, franchiseID int64) ([]media.FranchiseMember, error) {
	row, err := s.Franchises.Get(ctx, franchiseID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, media.ErrNoFranchise
	}
	if err != nil {
		return nil, err
	}
	books, err := s.seriesOf(ctx, row)
	if err != nil {
		return nil, err
	}
	service := s.Catalog.Service
	var missing []media.FranchiseMember
	for _, book := range books {
		held := slices.ContainsFunc(book.OpenLibrary, func(id string) bool {
			_, held := service.WorkByIDs(ctx, IDs{OpenLibrary: id})
			return held || service.IsExcluded(ctx, IDs{OpenLibrary: id})
		})
		if held || len(book.OpenLibrary) == 0 {
			continue
		}
		missing = append(missing, media.FranchiseMember{
			Title: book.Title, Year: book.Year, Provider: service.Format.Provider(ProviderOpenLibrary), ExternalID: book.OpenLibrary[0],
		})
	}
	return missing, nil
}

// seriesOf lists the books of the collection's series, in series order: the series named like the collection when its
// books are in several, else the one most of them are in.
func (s SeriesBooks) seriesOf(ctx context.Context, row library.FranchiseRow) ([]wikidata.SeriesBook, error) {
	if s.Source == nil {
		return nil, nil
	}
	members, err := s.Franchises.MembersOf(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	provider := s.Catalog.Service.Format.Provider(ProviderOpenLibrary)
	var held []string
	for _, workID := range members {
		if ids, err := s.Reader.ProviderIDs(ctx, workID); err == nil && ids[provider] != "" {
			held = append(held, ids[provider])
		}
	}
	books, err := s.Source.BookSeries(ctx, held)
	if err != nil || len(books) == 0 {
		return nil, err
	}
	chosen, _, _ := chooseSeries(books, held, row.Name)
	books = slices.DeleteFunc(books, func(book wikidata.SeriesBook) bool { return book.SeriesQID != chosen })
	slices.SortStableFunc(books, func(a, b wikidata.SeriesBook) int {
		return cmp.Or(cmp.Compare(ordinal(a.Ordinal), ordinal(b.Ordinal)), cmp.Compare(a.Year, b.Year))
	})
	return books, nil
}

// chooseSeries picks a collection's series among those its held books are in: the one named like the collection, else
// the one most of them are in; it answers how many held books that series has and whether its name is the collection's.
func chooseSeries(books []wikidata.SeriesBook, held []string, name string) (qid string, inside int, named bool) {
	count := map[string]int{}
	for _, book := range books {
		if slices.ContainsFunc(book.OpenLibrary, func(id string) bool { return slices.Contains(held, id) }) {
			count[book.SeriesQID]++
		}
		if foldTitle(book.SeriesName) == foldTitle(name) {
			qid, named = book.SeriesQID, true
		}
	}
	if qid == "" {
		for candidate, n := range count {
			if qid == "" || n > count[qid] || (n == count[qid] && candidate < qid) {
				qid = candidate
			}
		}
	}
	return qid, count[qid], named
}

// ordinal places a book by its number in the series, one without a number after the numbered ones.
func ordinal(value string) float64 {
	if number, err := strconv.ParseFloat(value, 64); err == nil {
		return number
	}
	return 1e9
}

// FranchiseFacts implements media.Franchising: Wikidata has no series covers, so a series shows its first book's.
func (SeriesBooks) FranchiseFacts(context.Context, []media.FranchiseRef) map[int64]media.FranchiseFact {
	return nil
}

// MonitorFranchise implements media.Franchising: monitoring a series adds its missing books, monitored, never past an
// exclusion, and answers how many.
func (s SeriesBooks) MonitorFranchise(ctx context.Context, categoryID, franchiseID int64, monitored bool) (int, error) {
	added := 0
	if monitored {
		missing, err := s.MissingFranchiseMembers(ctx, categoryID, franchiseID)
		if err != nil {
			return 0, err
		}
		catalog := works.Catalog{Source: catalogSource{s.Catalog.Service}, Categories: s.Catalog.Categories, DefaultProfileID: s.Catalog.DefaultProfileID}
		for _, member := range missing {
			if _, err := catalog.AddWork(ctx, media.AddRequest{
				CategoryID: categoryID, Kind: s.Catalog.Service.Format.MediaType, Provider: member.Provider, ExternalID: member.ExternalID, Monitored: true,
			}); err != nil {
				return added, err
			}
			added++
		}
	}
	return added, s.Franchises.SetMonitored(ctx, franchiseID, monitored, 0)
}
