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

// ListedBook is a book of a series a book provider lists, with its place in the series.
type ListedBook struct {
	ID       string
	Title    string
	Year     int
	Position float64
}

// SeriesLister is a provider that lists the books of a series by its name (Hardcover, once a key is configured).
type SeriesLister interface {
	ListSeries(ctx context.Context, name string) ([]ListedBook, error)
}

// TitleReader answers a work's title.
type TitleReader interface {
	TitleOf(ctx context.Context, workID int64) (string, error)
}

// SeriesBooks is a book format's part of a series collection: the series' books Wikidata (always) and the book provider
// (when it has a key) list that the library lacks, and monitoring the series, which adds them.
type SeriesBooks struct {
	Franchises library.Franchises
	Reader     library.SeriesReader
	Source     SeriesSource
	// Lister and Titles are optional: without a Lister only Wikidata's list is used, without Titles a held book is told from a listed one by its ids alone.
	Lister  SeriesLister
	Titles  TitleReader
	Catalog Catalog
}

var _ media.Franchising = SeriesBooks{}

// seriesMember is one book of the whole series, held or not, with the id it can be added by.
type seriesMember struct {
	title      string
	year       int
	ordinal    float64
	provider   string
	externalID string
	held       bool
}

// MissingFranchiseMembers implements media.Franchising: the books of the collection's series, in series order, that the
// library holds under none of their ids and a person did not exclude.
func (s SeriesBooks) MissingFranchiseMembers(ctx context.Context, _, franchiseID int64) ([]media.FranchiseMember, error) {
	members, err := s.members(ctx, franchiseID)
	if err != nil {
		return nil, err
	}
	var missing []media.FranchiseMember
	for _, member := range members {
		if member.held || member.externalID == "" {
			continue
		}
		missing = append(missing, media.FranchiseMember{Title: member.title, Year: member.year, Provider: member.provider, ExternalID: member.externalID})
	}
	return missing, nil
}

// members is the whole series of a collection in series order: Wikidata's books, completed by the book provider's list
// where it has a book Wikidata lacks or one without an Open Library id.
func (s SeriesBooks) members(ctx context.Context, franchiseID int64) ([]seriesMember, error) {
	row, err := s.Franchises.Get(ctx, franchiseID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, media.ErrNoFranchise
	}
	if err != nil {
		return nil, err
	}
	service := s.Catalog.Service
	heldTitles := s.heldTitles(ctx, row.ID)
	isHeld := func(title string, ids IDs) bool {
		_, held := service.WorkByIDs(ctx, ids)
		return held || service.IsExcluded(ctx, ids) || heldTitles[foldTitle(title)]
	}
	books, err := s.seriesOf(ctx, row)
	if err != nil {
		return nil, err
	}
	var members []seriesMember
	for _, book := range books {
		member := seriesMember{title: book.Title, year: book.Year, ordinal: ordinal(book.Ordinal)}
		if len(book.OpenLibrary) > 0 {
			member.provider, member.externalID = service.Format.Provider(ProviderOpenLibrary), book.OpenLibrary[0]
		}
		member.held = heldTitles[foldTitle(book.Title)] || slices.ContainsFunc(book.OpenLibrary, func(id string) bool { return isHeld(book.Title, IDs{OpenLibrary: id}) })
		members = append(members, member)
	}
	if s.Lister != nil {
		listed, err := s.Lister.ListSeries(ctx, row.Name)
		if err != nil {
			// A provider that cannot answer leaves the list as Wikidata gave it; the next look asks again.
			listed = nil
		}
		for _, book := range listed {
			at := slices.IndexFunc(members, func(member seriesMember) bool { return foldTitle(member.title) == foldTitle(book.Title) })
			if at >= 0 && members[at].externalID != "" {
				continue
			}
			held := isHeld(book.Title, IDs{Hardcover: book.ID})
			if at >= 0 {
				members[at].provider, members[at].externalID = service.Format.Provider(ProviderHardcover), book.ID
				members[at].held = members[at].held || held
				continue
			}
			members = append(members, seriesMember{
				title: book.Title, year: book.Year, ordinal: ordinalOf(book.Position),
				provider: service.Format.Provider(ProviderHardcover), externalID: book.ID, held: held,
			})
		}
	}
	slices.SortStableFunc(members, func(a, b seriesMember) int {
		return cmp.Or(cmp.Compare(a.ordinal, b.ordinal), cmp.Compare(a.year, b.year))
	})
	return members, nil
}

// heldTitles are the folded titles of the books the collection holds.
func (s SeriesBooks) heldTitles(ctx context.Context, franchiseID int64) map[string]bool {
	titles := map[string]bool{}
	if s.Titles == nil {
		return titles
	}
	members, err := s.Franchises.MembersOf(ctx, franchiseID)
	if err != nil {
		return titles
	}
	for _, workID := range members {
		if title, err := s.Titles.TitleOf(ctx, workID); err == nil {
			titles[foldTitle(title)] = true
		}
	}
	return titles
}

// ordinalOf places a listed book by its position, one without a position after the numbered ones.
func ordinalOf(position float64) float64 {
	if position <= 0 {
		return 1e9
	}
	return position
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

// FranchiseFacts implements media.Franchising: Wikidata has no series covers, so a series shows its first book's; the
// count is how many books the whole series has, held or not, which is what the collection page measures what it holds against.
func (s SeriesBooks) FranchiseFacts(ctx context.Context, franchises []media.FranchiseRef) map[int64]media.FranchiseFact {
	found := map[int64]media.FranchiseFact{}
	for _, franchise := range franchises {
		if members, err := s.members(ctx, franchise.ID); err == nil && len(members) > 0 {
			found[franchise.ID] = media.FranchiseFact{Total: len(members)}
		}
	}
	return found
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
				Fallback: &media.Fallback{Title: member.Title, Year: member.Year},
			}); err != nil {
				return added, err
			}
			added++
		}
	}
	return added, s.Franchises.SetMonitored(ctx, franchiseID, monitored, 0)
}
