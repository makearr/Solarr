package book

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

// MonitorOption is Readarr's "Monitor" choice when a book is added.
type MonitorOption string

// The book alone, the book and the rest of its series, or nothing.
const (
	MonitorBook          MonitorOption = "book_only"
	MonitorBookAndSeries MonitorOption = "book_and_series"
	MonitorNone          MonitorOption = "none"
)

// Valid reports whether the option is one of the three.
func (o MonitorOption) Valid() bool {
	return o == MonitorBook || o == MonitorBookAndSeries || o == MonitorNone
}

// releaseKind is the work_date kind the planner, the calendar and "Not out yet" read; one date per book.
const releaseKind = "release"

// Fields of a book other than the base ones, as stored in work_field.
const (
	FieldStatus         = "status"
	FieldSubtitle       = "subtitle"
	FieldPublisher      = "publisher"
	FieldPages          = "pages"
	FieldSeriesPosition = "series_position"
	FieldSeries         = "series"
	FieldAbridged       = "abridged"
	FieldPublished      = "published"
	FieldImages         = "images"
)

// ToWrite turns a merged book into what the library stores; now decides whether it is out yet.
func ToWrite(format Format, book Book, now time.Time) library.SeriesWrite {
	var yearPtr, runtimePtr *int64
	if book.Year > 0 {
		year := int64(book.Year)
		yearPtr = &year
	}
	if book.RuntimeMin > 0 {
		runtime := int64(book.RuntimeMin) * 60_000
		runtimePtr = &runtime
	}
	source := ""
	if len(book.Sources) > 0 {
		source = book.Sources[0]
	}
	write := library.SeriesWrite{
		Work:             library.WorkIdentity{MediaType: format.MediaType, CanonicalTitle: book.Title, SortTitle: SortTitle(book.Title)},
		Year:             yearPtr,
		OriginalLanguage: book.Language,
		RuntimeMs:        runtimePtr,
		Description:      book.Overview,
		IDs:              format.idWrites(book.IDs),
		Aliases:          book.Aliases,
		AliasSource:      "book-merge",
		Genres:           book.Genres,
		GenreSource:      source,
		GenreTree:        GenreTree.Key,
		Network:          book.Publisher,
		OrganisationKind: "publisher",
		// A book carries no certification Solarr can read: the rating limit leaves it alone.
		CertificationAge: -1,
		Fields:           fieldWrites(book, source, now),
		LeadRole:         RoleAuthor,
	}
	for i, rating := range book.Ratings {
		entry := library.SeriesRatingWrite{Provider: rating.Provider, Score: rating.Score, Votes: rating.Votes}
		if i == 0 {
			write.Rating = &entry
		} else {
			write.MoreRatings = append(write.MoreRatings, entry)
		}
	}
	if day := releaseDay(book); day != "" {
		write.Dates = append(write.Dates, library.SeriesDateWrite{Kind: releaseKind, Date: day, Source: source})
	}
	for _, credit := range book.Credits {
		key := PersonKey(credit.Name)
		if key == "" {
			continue
		}
		write.Credits = append(write.Credits, library.SeriesCreditWrite{
			Provider: ProviderPerson, ExternalID: key, Name: strings.TrimSpace(credit.Name), ImageURL: credit.Image, Role: credit.Role, Ord: credit.Order,
		})
	}
	if book.Series != nil && book.Series.Name != "" {
		// Keyed by name: Audible gives one series several ASINs, which made it several collections.
		write.Franchise = &library.SeriesFranchiseWrite{Provider: ProviderSeries, ExternalID: PersonKey(book.Series.Name), Name: book.Series.Name}
	}
	return write
}

// releaseDay is the day the book is out: this reading's or edition's date when it is a full one, else the first of its
// year of first publication. A book with no date is wanted at once.
func releaseDay(book Book) string {
	if len(book.Published) >= 10 {
		return book.Published[:10]
	}
	if book.Year > 0 {
		return strconv.Itoa(book.Year) + "-01-01"
	}
	return ""
}

// StatusOn says whether the book is out at now.
func (b Book) StatusOn(now time.Time) Status {
	day := releaseDay(b)
	if day == "" {
		return StatusPublished
	}
	at, err := time.Parse("2006-01-02", day)
	if err != nil || !at.After(now) {
		return StatusPublished
	}
	return StatusAnnounced
}

// Provider is the key a work id of the format is stored under. A book's work ids name the writing, which its audiobook
// shares, and an id belongs to one work: an audiobook keeps them under keys of its own, as TMDB's films do
// ("tmdb:movie"). An ASIN names the reading itself.
func (f Format) Provider(base string) string {
	if !f.ByFolder || base == ProviderAudible {
		return base
	}
	return base + ":" + f.MediaType
}

func (f Format) idWrites(ids IDs) []library.ExternalIDWrite {
	var writes []library.ExternalIDWrite
	add := func(provider, id string) {
		if id != "" {
			writes = append(writes, library.ExternalIDWrite{Provider: f.Provider(provider), ExternalID: id})
		}
	}
	add(ProviderAudible, ids.ASIN)
	add(ProviderOpenLibrary, ids.OpenLibrary)
	add(ProviderGoogleBooks, ids.GoogleBooks)
	add(ProviderISBN, ids.ISBN)
	add(ProviderGoodreads, ids.Goodreads)
	return writes
}

// idsOf reads a stored work's ids back.
func (f Format) idsOf(provider map[string]string) IDs {
	return IDs{
		OpenLibrary: provider[f.Provider(ProviderOpenLibrary)], ASIN: provider[ProviderAudible], GoogleBooks: provider[f.Provider(ProviderGoogleBooks)],
		ISBN: provider[f.Provider(ProviderISBN)], Goodreads: provider[f.Provider(ProviderGoodreads)],
	}
}

func fieldWrites(book Book, source string, now time.Time) []library.SeriesFieldWrite {
	var fields []library.SeriesFieldWrite
	add := func(field, value string) {
		if strings.TrimSpace(value) != "" {
			fields = append(fields, library.SeriesFieldWrite{Field: field, Provider: source, Value: value})
		}
	}
	add(FieldStatus, string(book.StatusOn(now)))
	add(FieldSubtitle, book.Subtitle)
	add(FieldPublisher, book.Publisher)
	add(FieldPublished, book.Published)
	if book.Pages > 0 {
		add(FieldPages, strconv.Itoa(book.Pages))
	}
	if book.Abridged {
		add(FieldAbridged, "true")
	}
	if book.Series != nil {
		add(FieldSeries, book.Series.Name)
		add(FieldSeriesPosition, book.Series.Position)
	}
	if len(book.Images) > 0 {
		if encoded, err := json.Marshal(book.Images); err == nil {
			add(FieldImages, string(encoded))
		}
	}
	return fields
}

// SortTitle lowercases a title, collapses whitespace and drops a leading article.
func SortTitle(title string) string {
	lowered := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(title))), " ")
	for _, article := range []string{"the ", "a ", "an "} {
		if rest, ok := strings.CutPrefix(lowered, article); ok {
			return rest
		}
	}
	return lowered
}
