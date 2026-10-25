package book

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/parsebook"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/audible"
	"github.com/makearr/Solarr/internal/metadata/googlebooks"
	"github.com/makearr/Solarr/internal/metadata/openlibrary"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ErrNoSource means no provider could be asked or none knew the book.
var ErrNoSource = errors.New("book: no metadata source answered")

// OpenLibrarySource is the Open Library client as the service needs it.
type OpenLibrarySource interface {
	Search(ctx context.Context, title, author string, limit int) ([]openlibrary.Doc, error)
	Doc(ctx context.Context, workID string) (openlibrary.Doc, error)
	Work(ctx context.Context, workID string) (openlibrary.Work, error)
	ByISBN(ctx context.Context, isbn string) (openlibrary.Edition, error)
}

// GoogleBooksSource is the Google Books client as the service needs it.
type GoogleBooksSource interface {
	Volume(ctx context.Context, id string) (googlebooks.Volume, error)
	Search(ctx context.Context, title, author string, limit int) ([]googlebooks.Volume, error)
	ByISBN(ctx context.Context, isbn string) (googlebooks.Volume, error)
}

// AudibleSource is the Audible client as the service needs it.
type AudibleSource interface {
	Search(ctx context.Context, title, author string, limit int) ([]audible.Product, error)
	Book(ctx context.Context, asin string) (audible.Book, error)
}

// IDBridge is Wikidata as a fetch asks it: the ids its item records for a book, and what it says of it.
type IDBridge interface {
	BookIDs(ctx context.Context, openLibrary string) (wikidata.WorkIDs, bool, error)
	FactsOf(ctx context.Context, kind wikidata.FactKind, ids []string) (map[string]wikidata.WorkFacts, error)
}

// Service reads books of one format from the providers and keeps them in the library. Any source may be nil.
type Service struct {
	Format      Format
	OpenLibrary OpenLibrarySource
	GoogleBooks GoogleBooksSource
	Audible     AudibleSource
	Wikidata    IDBridge
	// PriorityFor is a library's provider order; nil, or an empty answer, takes the format's.
	PriorityFor func(categoryID int64) []string
	Series      library.Series
	Reader      library.SeriesReader
	Desired     library.Desired
	Franchises  library.Franchises
	Exclusions  library.Exclusions
	Clock       clock.Clock
	Logger      *slog.Logger
}

func (s Service) warn(source, id string, err error) {
	if s.Logger != nil {
		s.Logger.Warn("book metadata source failed", slog.String("media_type", s.Format.MediaType), slog.String("source", source),
			slog.String("id", id), slog.Any("error", err))
	}
}

// order is the providers a library asks, best first.
func (s Service) order(categoryID int64) []string {
	if s.PriorityFor != nil && categoryID != 0 {
		if order := s.PriorityFor(categoryID); len(order) > 0 {
			return order
		}
	}
	return s.Format.Sources
}

// Hint is what is known of a book besides its ids, so a provider without one of them can still be asked.
type Hint struct {
	Title, Author string
}

// FetchFor merges every source the library asks that knows the book, the first supplying what it holds and each later
// one only filling what is missing. A failing source is skipped; only all failing is an error.
func (s Service) FetchFor(ctx context.Context, categoryID int64, ids IDs, hint Hint) (Book, error) {
	order := s.order(categoryID)
	answers := map[string]Book{}
	if ask := slices.Contains(order, settings.BookSourceAudible); ask && s.Audible != nil && ids.ASIN != "" {
		if found, err := s.Audible.Book(ctx, ids.ASIN); err == nil {
			answers[settings.BookSourceAudible] = FromAudible(found)
			heard := answers[settings.BookSourceAudible]
			ids.ISBN = cmp.Or(ids.ISBN, heard.IDs.ISBN)
			hint = Hint{Title: cmp.Or(hint.Title, heard.Title), Author: cmp.Or(hint.Author, first(heard.Authors()))}
		} else if !errors.Is(err, audible.ErrNotFound) {
			s.warn(sourceAudible, ids.ASIN, err)
		}
	}
	ids = s.bridgeIDs(ctx, s.completeIDs(ctx, ids, hint), order)
	// A provider early in the order may lack the id a later one answers with (Google Books' from Open Library's ISBNs):
	// it is asked again once the first round has taught the ids it needs.
	for round := 0; round < 2; round++ {
		before := ids
		for _, name := range order {
			if _, asked := answers[name]; asked {
				continue
			}
			if answer, ok := s.ask(ctx, name, ids); ok {
				answers[name] = answer
				ids = Merge(Book{IDs: ids}, Book{IDs: answer.IDs}).IDs
			}
		}
		if ids == before {
			break
		}
	}
	var book Book
	found := false
	for _, name := range order {
		answer, ok := answers[name]
		switch {
		case !ok:
		case !found:
			book, found = answer, true
		default:
			book = Merge(book, answer)
		}
	}
	// Wikidata names no title of its own (its title is the original language's): a book only it knew is not known.
	if !found || book.Title == "" {
		return Book{}, ErrNoSource
	}
	book.IDs = Merge(Book{IDs: book.IDs}, Book{IDs: ids}).IDs
	if len(book.Genres) == 0 {
		book.Genres = book.spareGenres
	}
	return book, nil
}

// ask asks one provider by the ids known; one without the id it needs, or that knows nothing, answers none.
func (s Service) ask(ctx context.Context, name string, ids IDs) (Book, bool) {
	report := func(id string, err error, missing error) bool {
		if err != nil && !errors.Is(err, missing) {
			s.warn(name, id, err)
		}
		return err == nil
	}
	switch name {
	case settings.SourceWikidata:
		if s.Wikidata != nil && ids.OpenLibrary != "" {
			facts, err := s.Wikidata.FactsOf(ctx, wikidata.FactBook, []string{ids.OpenLibrary})
			found, known := facts[ids.OpenLibrary]
			return FromWikidata(found), report(ids.OpenLibrary, err, nil) && known
		}
	case settings.BookSourceOpenLibrary:
		if s.OpenLibrary != nil && ids.OpenLibrary != "" {
			work, err := s.OpenLibrary.Work(ctx, ids.OpenLibrary)
			if !report(ids.OpenLibrary, err, openlibrary.ErrNotFound) {
				return Book{}, false
			}
			doc, err := s.OpenLibrary.Doc(ctx, ids.OpenLibrary)
			report(ids.OpenLibrary, err, openlibrary.ErrNotFound)
			return FromOpenLibrary(work, doc), true
		}
	case settings.BookSourceGoogleBooks:
		if s.GoogleBooks == nil {
			return Book{}, false
		}
		switch {
		case ids.GoogleBooks != "":
			volume, err := s.GoogleBooks.Volume(ctx, ids.GoogleBooks)
			return FromGoogleBooks(volume), report(ids.GoogleBooks, err, googlebooks.ErrNotFound)
		case ids.ISBN != "":
			volume, err := s.GoogleBooks.ByISBN(ctx, ids.ISBN)
			return FromGoogleBooks(volume), report(ids.ISBN, err, googlebooks.ErrNotFound)
		}
	case settings.BookSourceAudible:
		if s.Audible != nil && ids.ASIN != "" {
			found, err := s.Audible.Book(ctx, ids.ASIN)
			return FromAudible(found), report(ids.ASIN, err, audible.ErrNotFound)
		}
	}
	return Book{}, false
}

// completeIDs finds Open Library's work for a book known only by an ISBN or, for an audiobook, by the title and author
// of its reading: the one search hit with exactly that title by that author.
func (s Service) completeIDs(ctx context.Context, ids IDs, hint Hint) IDs {
	if ids.OpenLibrary != "" || s.OpenLibrary == nil {
		return ids
	}
	if ids.ISBN != "" {
		edition, err := s.OpenLibrary.ByISBN(ctx, ids.ISBN)
		if err == nil && edition.WorkID() != "" {
			ids.OpenLibrary = edition.WorkID()
			return ids
		}
		if err != nil && !errors.Is(err, openlibrary.ErrNotFound) {
			s.warn(sourceOpenLibrary, ids.ISBN, err)
		}
	}
	if hint.Title == "" || hint.Author == "" {
		return ids
	}
	docs, err := s.OpenLibrary.Search(ctx, hint.Title, hint.Author, 5)
	if err != nil {
		s.warn(sourceOpenLibrary, hint.Title, err)
		return ids
	}
	var matched []openlibrary.Doc
	for _, doc := range docs {
		if parsebook.CleanTitle(doc.Title) == parsebook.CleanTitle(hint.Title) && slices.ContainsFunc(doc.AuthorNames, func(name string) bool {
			return PersonKey(name) == PersonKey(hint.Author)
		}) {
			matched = append(matched, doc)
		}
	}
	if len(matched) == 1 {
		ids.OpenLibrary = matched[0].WorkID()
	}
	return ids
}

// bridgeIDs fills in the ids Wikidata's item records for the book and the book lacks; first in the library's order,
// Wikidata also replaces the Google Books id it disagrees with. Open Library's id found the item and is never changed.
func (s Service) bridgeIDs(ctx context.Context, ids IDs, order []string) IDs {
	at := slices.Index(order, settings.SourceWikidata)
	if s.Wikidata == nil || at < 0 || ids.OpenLibrary == "" {
		return ids
	}
	found, ok, err := s.Wikidata.BookIDs(ctx, ids.OpenLibrary)
	if err != nil {
		s.warn(sourceWikidata, ids.OpenLibrary, err)
		return ids
	}
	if !ok {
		return ids
	}
	if found.GoogleBooks != "" && (ids.GoogleBooks == "" || at == 0) {
		ids.GoogleBooks = found.GoogleBooks
	}
	// An audiobook's ISBN is its own reading's, never the printed book's that Wikidata records.
	if ids.ISBN == "" && !s.Format.ByFolder {
		ids.ISBN = found.ISBN
	}
	ids.Goodreads = cmp.Or(ids.Goodreads, found.Goodreads)
	return ids
}

// Warmer is Wikidata read in bulk ahead of the lookups that follow.
type Warmer interface {
	Warm(ctx context.Context, kind wikidata.FactKind, ids []string) error
}

// WarmWikidata reads at once what Wikidata says of every book about to be refreshed whose library asks it.
func (s Service) WarmWikidata(ctx context.Context, due []library.RefreshCandidate) {
	warmer, bulk := s.Wikidata.(Warmer)
	if !bulk {
		return
	}
	var ids []string
	for _, candidate := range due {
		if !slices.Contains(s.order(candidate.CategoryID), settings.SourceWikidata) {
			continue
		}
		if provider, err := s.Reader.ProviderIDs(ctx, candidate.WorkID); err == nil && s.Format.idsOf(provider).OpenLibrary != "" {
			ids = append(ids, s.Format.idsOf(provider).OpenLibrary)
		}
	}
	if err := warmer.Warm(ctx, wikidata.FactBook, ids); err != nil {
		s.warn(sourceWikidata, "", err)
	}
}

// Candidate is one book a search found.
type Candidate struct {
	IDs      IDs
	Title    string
	Author   string
	Year     int
	Overview string
	Cover    string
	Traits   media.Traits
}

// Search finds books by title and, when given, author: an audiobook in Audible's catalog first, a book in Open Library,
// either then in Google Books. An empty answer is not an error.
func (s Service) Search(ctx context.Context, categoryID int64, title, author string, limit int) ([]Candidate, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, nil
	}
	var failure error
	for _, name := range s.searchOrder(categoryID) {
		found, err := s.searchOne(ctx, name, title, author, limit)
		if err != nil {
			failure = err
			s.warn(name, title, err)
			continue
		}
		if len(found) > 0 {
			return found, nil
		}
	}
	if failure != nil {
		return nil, fmt.Errorf("book: search %q: %w", title, failure)
	}
	return nil, nil
}

// searchOrder is the library's providers that can search, in its order; Wikidata cannot.
func (s Service) searchOrder(categoryID int64) []string {
	var names []string
	for _, name := range s.order(categoryID) {
		if name != settings.SourceWikidata && (name != settings.BookSourceAudible || s.Format.ByFolder) {
			names = append(names, name)
		}
	}
	return names
}

func (s Service) searchOne(ctx context.Context, name, title, author string, limit int) ([]Candidate, error) {
	var candidates []Candidate
	switch name {
	case settings.BookSourceAudible:
		if s.Audible == nil {
			return nil, nil
		}
		products, err := s.Audible.Search(ctx, title, author, limit)
		if err != nil {
			return nil, err
		}
		for _, product := range products {
			candidate := Candidate{IDs: IDs{ASIN: product.ASIN}, Title: product.Title, Year: product.Year(), Overview: cleanText(product.Summary), Cover: product.Cover(),
				Traits: media.Traits{OriginalLanguage: language(product.Language)}}
			if len(product.Authors) > 0 {
				candidate.Author = product.Authors[0].Name
			}
			candidates = append(candidates, candidate)
		}
	case settings.BookSourceOpenLibrary:
		if s.OpenLibrary == nil {
			return nil, nil
		}
		docs, err := s.OpenLibrary.Search(ctx, title, author, limit)
		if err != nil {
			return nil, err
		}
		for _, doc := range docs {
			found := FromOpenLibrary(openlibrary.Work{}, doc)
			candidates = append(candidates, Candidate{IDs: IDs{OpenLibrary: doc.WorkID()}, Title: doc.Title, Author: first(doc.AuthorNames),
				Year: doc.FirstPublishYear, Cover: openlibrary.CoverURL(doc.CoverID), Traits: media.Traits{Genres: found.Genres, OriginalLanguage: found.Language}})
		}
	case settings.BookSourceGoogleBooks:
		if s.GoogleBooks == nil {
			return nil, nil
		}
		volumes, err := s.GoogleBooks.Search(ctx, title, author, limit)
		if err != nil {
			return nil, err
		}
		for _, volume := range volumes {
			found := FromGoogleBooks(volume)
			candidates = append(candidates, Candidate{IDs: found.IDs, Title: found.Title, Author: first(found.Authors()), Year: found.Year,
				Overview: found.Overview, Cover: volume.Cover(), Traits: media.Traits{Genres: found.Genres, OriginalLanguage: found.Language}})
		}
	}
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}

// AddRequest is one book a person asked to hold.
type AddRequest struct {
	IDs        IDs
	Hint       Hint
	CategoryID int64
	ProfileID  int64
	Monitor    MonitorOption
	AddedBy    *int64
	// Fallback is stored when no metadata source knows the book; nil refuses the add then.
	Fallback *Book
}

// AddResult is what an add stored.
type AddResult struct {
	WorkID  int64
	ItemID  int64
	Created bool
	Book    Book
}

// Add stores a book and its one wanted item. Adding what the library already holds is not an error: the book is
// refreshed and its item left as it is (Invariant 71).
func (s Service) Add(ctx context.Context, request AddRequest) (AddResult, error) {
	monitor := request.Monitor
	if monitor == "" {
		monitor = MonitorBook
	}
	if !monitor.Valid() {
		return AddResult{}, fmt.Errorf("book: unknown monitor option %q", monitor)
	}
	book, err := s.FetchFor(ctx, request.CategoryID, request.IDs, request.Hint)
	if errors.Is(err, ErrNoSource) && request.Fallback != nil {
		book, err = *request.Fallback, nil
	}
	if err != nil {
		return AddResult{}, err
	}
	now := s.Clock.Now()
	stored, err := s.Series.Upsert(ctx, now, ToWrite(s.Format, book, now))
	if err != nil {
		return AddResult{}, err
	}
	item, err := s.Desired.EnsureAlbumItem(ctx, library.AlbumRequest{
		WorkID: stored.WorkID, CategoryID: request.CategoryID, ProfileID: request.ProfileID,
		Monitored: monitor != MonitorNone, AddedBy: request.AddedBy, RequiredWithoutParts: 1,
	})
	if err != nil {
		return AddResult{}, fmt.Errorf("book: wanted item: %w", err)
	}
	if monitor == MonitorBookAndSeries {
		if err := s.Franchises.SetMonitoredOf(ctx, stored.WorkID, true); err != nil {
			return AddResult{}, err
		}
	}
	return AddResult{WorkID: stored.WorkID, ItemID: item.ID, Created: stored.Created, Book: book}, nil
}

// Refresh reads a book the library holds again. Monitoring and the person's own fields are untouched.
func (s Service) Refresh(ctx context.Context, categoryID, workID int64) (Book, error) {
	provider, err := s.Reader.ProviderIDs(ctx, workID)
	if err != nil {
		return Book{}, err
	}
	basics, err := s.Reader.Basics(ctx, workID)
	if err != nil {
		return Book{}, err
	}
	book, err := s.FetchFor(ctx, categoryID, s.Format.idsOf(provider), Hint{Title: basics.Title})
	if err != nil {
		return Book{}, err
	}
	now := s.Clock.Now()
	if _, err := s.Series.Upsert(ctx, now, ToWrite(s.Format, book, now)); err != nil {
		return Book{}, err
	}
	return book, nil
}

// ApplyMonitor sets a book's monitoring as Readarr's Monitor choice says; "Book and series" also follows its series.
func (s Service) ApplyMonitor(ctx context.Context, workID int64, option MonitorOption) error {
	if !option.Valid() {
		return fmt.Errorf("book: unknown monitor option %q", option)
	}
	items, err := s.Desired.AlbumItemsForWork(ctx, workID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := s.Desired.SetItemMonitored(ctx, item.ID, option != MonitorNone); err != nil {
			return err
		}
	}
	if option == MonitorBookAndSeries {
		return s.Franchises.SetMonitoredOf(ctx, workID, true)
	}
	return nil
}

// IsExcluded reports whether a person removed this book and asked not to see it again.
func (s Service) IsExcluded(ctx context.Context, ids IDs) bool {
	excluded, err := s.Exclusions.Excluded(ctx, s.Format.idWrites(ids))
	if err != nil {
		s.warn("exclusion check", ids.OpenLibrary+ids.ASIN, err)
		return false
	}
	return excluded
}

// WorkByIDs is the work of the format a library holds under any of the ids.
func (s Service) WorkByIDs(ctx context.Context, ids IDs) (int64, bool) {
	for _, write := range s.Format.idWrites(ids) {
		if id, held, err := s.Series.WorkByProviderID(ctx, s.Format.MediaType, write.Provider, write.ExternalID); err == nil && held {
			return id, true
		}
	}
	return 0, false
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
