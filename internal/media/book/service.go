package book

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/parsebook"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/audible"
	"github.com/makearr/Solarr/internal/metadata/fantlab"
	"github.com/makearr/Solarr/internal/metadata/googlebooks"
	"github.com/makearr/Solarr/internal/metadata/hardcover"
	"github.com/makearr/Solarr/internal/metadata/itunes"
	"github.com/makearr/Solarr/internal/metadata/openlibrary"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNoSource means no provider could be asked or none knew the book.
var ErrNoSource = errors.New("book: no metadata source answered")

// OpenLibrarySource is the Open Library client as the service needs it.
type OpenLibrarySource interface {
	Search(ctx context.Context, title, author string, limit int) ([]openlibrary.Doc, error)
	ByAuthor(ctx context.Context, author string, limit int) ([]openlibrary.Doc, error)
	ByAuthorKey(ctx context.Context, authorID string, limit, offset int) ([]openlibrary.Doc, int, error)
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

// ITunesSource is the iTunes client as the service needs it.
type ITunesSource interface {
	Search(ctx context.Context, title, author string, limit int) ([]itunes.Audiobook, error)
	Lookup(ctx context.Context, id int64) (itunes.Audiobook, error)
}

// FantLabSource is the FantLab client as the service needs it.
type FantLabSource interface {
	Search(ctx context.Context, title string, limit int) ([]fantlab.Match, error)
	Work(ctx context.Context, id int64) (fantlab.Work, error)
}

// HardcoverSource is the Hardcover client as the service needs it.
type HardcoverSource interface {
	Configured() bool
	Search(ctx context.Context, title, author string, limit int) ([]hardcover.Book, error)
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
	ITunes      ITunesSource
	FantLab     FantLabSource
	Hardcover   HardcoverSource
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
	if s.Logger != nil && errors.Is(err, httpx.ErrCircuitOpen) {
		// A paused provider is shown once as its health, not per book it skipped (Google Books' daily quota).
		s.Logger.Debug("book metadata source paused", slog.String("source", source), slog.String("id", id))
		return
	}
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
	var failed []string
	if ask := slices.Contains(order, settings.BookSourceAudible); ask && s.Audible != nil && ids.ASIN != "" {
		if found, err := s.Audible.Book(ctx, ids.ASIN); err == nil {
			answers[settings.BookSourceAudible] = FromAudible(found)
			heard := answers[settings.BookSourceAudible]
			ids.ISBN = cmp.Or(ids.ISBN, heard.IDs.ISBN)
			hint = Hint{Title: cmp.Or(hint.Title, heard.Title), Author: cmp.Or(hint.Author, first(heard.Authors()))}
		} else if !errors.Is(err, audible.ErrNotFound) {
			s.warn(sourceAudible, ids.ASIN, err)
			failed = append(failed, sourceAudible)
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
			if answer, ok := s.ask(ctx, name, ids, hintFrom(hint, answers, order), &failed); ok {
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
	slices.Sort(failed)
	book.Failed = slices.Compact(failed)
	return book, nil
}

// ask asks one provider by the ids known; one without the id it needs, or that knows nothing, answers none.
func (s Service) ask(ctx context.Context, name string, ids IDs, hint Hint, failed *[]string) (Book, bool) {
	report := func(id string, err error, missing error) bool {
		if err != nil && !errors.Is(err, missing) {
			s.warn(name, id, err)
			*failed = append(*failed, name)
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
	case settings.BookSourceITunes:
		return s.askITunes(ctx, ids, hint, report)
	case settings.BookSourceFantLab:
		return s.askFantLab(ctx, ids, hint, report)
	case settings.BookSourceHardcover:
		return s.askHardcover(ctx, ids, hint, report)
	}
	return Book{}, false
}

// hintFrom is the hint, with the title and author the sources asked first supplying what it lacks: a provider that
// finds a book by its name needs one.
func hintFrom(hint Hint, answers map[string]Book, order []string) Hint {
	for _, name := range order {
		if hint.Title != "" && hint.Author != "" {
			break
		}
		if answer, ok := answers[name]; ok {
			hint = Hint{Title: cmp.Or(hint.Title, answer.Title), Author: cmp.Or(hint.Author, first(answer.Authors()))}
		}
	}
	return hint
}

// sameTitle says a provider's title is the hint's: the same letters and digits, or one title followed by a subtitle
// ("Dune: Deluxe Edition", "Dune (Unabridged)"); another word after it makes a neighbouring book ("Dune Messiah").
func sameTitle(found, wanted string) bool {
	a, b := foldTitle(found), foldTitle(wanted)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	shorter, longer := strings.ToLower(strings.TrimSpace(wanted)), strings.ToLower(strings.TrimSpace(found))
	if len(a) < len(b) {
		shorter, longer = longer, shorter
	}
	rest, continues := strings.CutPrefix(longer, shorter)
	return continues && strings.ContainsRune(":-–—([", firstRune(strings.TrimSpace(rest)))
}

func firstRune(text string) rune {
	for _, r := range text {
		return r
	}
	return 0
}

// bestTitleHit is the hit with the hinted title and author: the exact title first, else the shortest title that
// continues it, so a search listing a longer neighbour first does not adopt it.
func bestTitleHit[T any](hits []T, wanted string, titles func(T) []string, authorOK func(T) bool) (T, bool) {
	var best T
	bestLength, found := 0, false
	for _, hit := range hits {
		if !authorOK(hit) {
			continue
		}
		for _, title := range titles(hit) {
			if !sameTitle(title, wanted) {
				continue
			}
			length := len(foldTitle(title))
			if !found || length < bestLength {
				best, bestLength, found = hit, length, true
			}
		}
	}
	return best, found
}

// sameAuthor says the found book is by the hinted author, or that no author was hinted.
func sameAuthor(found []string, wanted string) bool {
	if wanted == "" {
		return true
	}
	return slices.ContainsFunc(found, func(name string) bool { return PersonKey(name) == PersonKey(wanted) })
}

func (s Service) askITunes(ctx context.Context, ids IDs, hint Hint, report func(string, error, error) bool) (Book, bool) {
	if s.ITunes == nil || !s.Format.ByFolder {
		return Book{}, false
	}
	if id, err := strconv.ParseInt(ids.ITunes, 10, 64); err == nil {
		found, err := s.ITunes.Lookup(ctx, id)
		return FromITunes(found), report(ids.ITunes, err, itunes.ErrNotFound)
	}
	if hint.Title == "" {
		return Book{}, false
	}
	hits, err := s.ITunes.Search(ctx, hint.Title, hint.Author, 5)
	if !report(hint.Title, err, nil) {
		return Book{}, false
	}
	hit, found := bestTitleHit(hits, hint.Title, func(hit itunes.Audiobook) []string { return []string{hit.Title()} },
		func(hit itunes.Audiobook) bool { return sameAuthor([]string{hit.Author}, hint.Author) })
	if !found {
		return Book{}, false
	}
	return FromITunes(hit), true
}

func (s Service) askFantLab(ctx context.Context, ids IDs, hint Hint, report func(string, error, error) bool) (Book, bool) {
	if s.FantLab == nil {
		return Book{}, false
	}
	id, err := strconv.ParseInt(ids.FantLab, 10, 64)
	if err != nil {
		if hint.Title == "" {
			return Book{}, false
		}
		hits, err := s.FantLab.Search(ctx, hint.Title, 5)
		if !report(hint.Title, err, nil) {
			return Book{}, false
		}
		hit, found := bestTitleHit(hits, hint.Title, func(hit fantlab.Match) []string { return []string{hit.Name, hit.RussianName} },
			func(hit fantlab.Match) bool { return sameAuthor([]string{hit.AuthorName, hit.AuthorRus}, hint.Author) })
		if found {
			id = hit.WorkID
		}
	}
	if id == 0 {
		return Book{}, false
	}
	work, err := s.FantLab.Work(ctx, id)
	return FromFantLab(work), report(strconv.FormatInt(id, 10), err, fantlab.ErrNotFound)
}

func (s Service) askHardcover(ctx context.Context, ids IDs, hint Hint, report func(string, error, error) bool) (Book, bool) {
	if s.Hardcover == nil || !s.Hardcover.Configured() || hint.Title == "" {
		return Book{}, false
	}
	hits, err := s.Hardcover.Search(ctx, hint.Title, hint.Author, 5)
	if !report(hint.Title, err, nil) {
		return Book{}, false
	}
	if i := slices.IndexFunc(hits, func(hit hardcover.Book) bool { return ids.Hardcover != "" && hit.ID == ids.Hardcover }); i >= 0 {
		return FromHardcover(hits[i]), true
	}
	hit, found := bestTitleHit(hits, hint.Title, func(hit hardcover.Book) []string { return []string{hit.Title} },
		func(hit hardcover.Book) bool { return sameAuthor(hit.Authors, hint.Author) })
	if !found {
		return Book{}, false
	}
	return FromHardcover(hit), true
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
	IDs    IDs
	Title  string
	Author string
	// CoAuthors are the book's other authors, which only the match of a folder's author reads.
	CoAuthors []string
	// Translators are named apart from the authors, which they are not.
	Translators []string
	Year        int
	Overview    string
	Cover       string
	Traits      media.Traits
}

// Search finds books by title and, when given, author: an audiobook in Audible's catalog first, a book in Open Library,
// either then in Google Books. An empty answer is not an error.
func (s Service) Search(ctx context.Context, categoryID int64, title, author string, limit int) ([]Candidate, error) {
	return s.SearchUntil(ctx, categoryID, title, author, limit, nil)
}

// SearchUntil is Search that keeps asking the next source while enough says the hits so far do not settle it: a source
// answers almost any query with something, and the one holding the exact title may be behind it. A nil enough stops
// at the first source with an answer.
func (s Service) SearchUntil(ctx context.Context, categoryID int64, title, author string, limit int, enough func([]Candidate) bool) ([]Candidate, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, nil
	}
	var failure error
	var found []Candidate
	for _, name := range s.searchOrder(categoryID) {
		hits, err := s.searchOne(ctx, name, title, author, limit)
		if err != nil {
			failure = err
			s.warn(name, title, err)
			continue
		}
		if len(hits) == 0 {
			continue
		}
		found = append(found, hits...)
		if enough == nil || enough(found) {
			return found, nil
		}
	}
	if len(found) == 0 && failure != nil {
		return nil, fmt.Errorf("book: search %q: %w", title, failure)
	}
	return found, nil
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
			authors, translators := product.Credits()
			candidate.Author, candidate.CoAuthors, candidate.Translators = first(authors), othersThanFirst(authors), translators
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
			candidates = append(candidates, Candidate{IDs: IDs{OpenLibrary: doc.WorkID()}, Title: doc.Title, Author: first(doc.AuthorNames), CoAuthors: othersThanFirst(doc.AuthorNames),
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
			candidates = append(candidates, Candidate{IDs: found.IDs, Title: found.Title, Author: first(found.Authors()), CoAuthors: othersThanFirst(found.Authors()), Year: found.Year,
				Overview: found.Overview, Cover: volume.Cover(), Traits: media.Traits{Genres: found.Genres, OriginalLanguage: found.Language}})
		}
	case settings.BookSourceITunes:
		if s.ITunes == nil || !s.Format.ByFolder {
			return nil, nil
		}
		items, err := s.ITunes.Search(ctx, title, author, limit)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			found := FromITunes(item)
			candidates = append(candidates, Candidate{IDs: found.IDs, Title: found.Title, Author: first(found.Authors()), CoAuthors: othersThanFirst(found.Authors()), Year: found.Year,
				Overview: found.Overview, Cover: item.Cover(), Traits: media.Traits{Genres: found.Genres}})
		}
	case settings.BookSourceFantLab:
		if s.FantLab == nil {
			return nil, nil
		}
		matches, err := s.FantLab.Search(ctx, title, limit)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			candidates = append(candidates, Candidate{IDs: IDs{FantLab: strconv.FormatInt(match.WorkID, 10)}, Title: cmp.Or(match.Name, match.RussianName),
				Author: cmp.Or(match.AuthorName, match.AuthorRus), Year: match.Year, Cover: match.Cover()})
		}
	case settings.BookSourceHardcover:
		if s.Hardcover == nil || !s.Hardcover.Configured() {
			return nil, nil
		}
		books, err := s.Hardcover.Search(ctx, title, author, limit)
		if err != nil {
			return nil, err
		}
		for _, hit := range books {
			found := FromHardcover(hit)
			candidates = append(candidates, Candidate{IDs: found.IDs, Title: found.Title, Author: first(found.Authors()), CoAuthors: othersThanFirst(found.Authors()), Year: found.Year,
				Overview: found.Overview, Cover: hit.Cover(), Traits: media.Traits{Genres: found.Genres}})
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
	now := s.Clock.Now()
	// The stored author keeps a search by title from adopting a neighbouring book of another writer.
	hint := Hint{Title: basics.Title}
	if facts, err := NamingFacts(ctx, s.Reader, s.Format, categoryID, workID, now); err == nil && facts.Author != unknownAuthor {
		hint.Author = facts.Author
	}
	stored := s.Format.idsOf(provider)
	book, err := s.FetchFor(ctx, categoryID, stored, hint)
	if err != nil {
		return Book{}, err
	}
	if source, down := failedSupplier(stored, book.Failed); down {
		return Book{}, fmt.Errorf("book %d: refresh skipped, %s did not answer: %w", workID, source, media.ErrSourceUnavailable)
	}
	if refreshedAnotherBook(basics.Title, book.Title, stored, book.IDs) {
		return Book{}, fmt.Errorf("book %d: the refresh found %q for %q with other ids; nothing was stored: %w", workID, book.Title, basics.Title, ErrOtherBook)
	}
	if _, err := s.Series.Upsert(ctx, now, ToWrite(s.Format, book, now)); err != nil {
		return Book{}, err
	}
	return book, nil
}

// failedSupplier is a source that holds the book's stored id and did not answer: a refresh built on the others would
// replace what it supplied with their shorter lists.
func failedSupplier(stored IDs, failed []string) (string, bool) {
	holds := map[string]string{
		settings.BookSourceOpenLibrary: stored.OpenLibrary, settings.BookSourceGoogleBooks: stored.GoogleBooks, settings.BookSourceAudible: stored.ASIN,
		settings.BookSourceITunes: stored.ITunes, settings.BookSourceFantLab: stored.FantLab, settings.BookSourceHardcover: stored.Hardcover,
	}
	for _, name := range failed {
		if holds[name] != "" {
			return name, true
		}
	}
	return "", false
}

// ErrOtherBook says a refresh found a different book than the one stored; the person can still identify it by hand.
var ErrOtherBook = errors.New("book: refresh found another book")

// refreshedAnotherBook is true when the fetched book is titled differently and shares no provider id with the stored one.
func refreshedAnotherBook(storedTitle, fetchedTitle string, stored, fetched IDs) bool {
	if foldTitle(storedTitle) == foldTitle(fetchedTitle) || fetched.Empty() {
		return false
	}
	return !sharesAnID(stored, fetched)
}

// sharesAnID reports whether both sides hold the same non-empty id for some provider.
func sharesAnID(a, b IDs) bool {
	pairs := [][2]string{
		{a.OpenLibrary, b.OpenLibrary}, {a.GoogleBooks, b.GoogleBooks}, {a.ASIN, b.ASIN}, {a.ISBN, b.ISBN},
		{a.Goodreads, b.Goodreads}, {a.Hardcover, b.Hardcover}, {a.ITunes, b.ITunes}, {a.FantLab, b.FantLab},
	}
	return slices.ContainsFunc(pairs, func(pair [2]string) bool { return pair[0] != "" && pair[0] == pair[1] })
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
