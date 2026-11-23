package book

import (
	"context"
	"strings"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/specprovider"
)

// ConsumesRecords implements media.RecordConsuming for the book and audiobook types.
func (Module) ConsumesRecords() bool { return true }

var _ media.RecordConsuming = Module{}

// RecordProvider is a definition-file provider as the book service asks it.
type RecordProvider interface {
	Namespace() string
	Search(ctx context.Context, query specprovider.Query) ([]specprovider.Record, error)
	Item(ctx context.Context, id string) (specprovider.Record, error)
}

func (s Service) recordProvider(key string) RecordProvider {
	if s.Records == nil {
		return nil
	}
	return s.Records(key)
}

// FromRecord turns a definition's record into a book. The book's ids are typed fields with no place for a definition's own
// id, so only the ids of providers Solarr names are kept; a definition is asked again by title and author.
func FromRecord(key string, record specprovider.Record) Book {
	book := Book{
		Title: record.Title, Overview: cleanText(record.Overview), Year: record.Year, Language: record.Language, Pages: record.PageCount,
		RuntimeMin: record.RuntimeSeconds / 60, Genres: genresOf(append(append([]string{}, record.Genres...), record.Tags...)), Sources: []string{key},
		Aliases: aliasesOf(record.Title, append([]string{record.OriginalTitle}, record.AltTitles...)),
	}
	book.IDs.ISBN = firstISBN13([]string{record.IDs["isbn"], record.IDs["isbn13"]})
	book.IDs.ASIN = record.IDs["asin"]
	book.IDs.OpenLibrary = record.IDs["openlibrary"]
	if len(record.Date) >= 10 {
		book.Published = record.Date[:10]
	}
	for _, organisation := range record.Organisations {
		if book.Publisher == "" || strings.EqualFold(organisation.Role, "publisher") {
			book.Publisher = organisation.Name
		}
	}
	for _, image := range record.Images {
		kind := "poster"
		if image.Kind == "backdrop" || image.Kind == "banner" {
			kind = image.Kind
		}
		book.Images = append(book.Images, Image{Kind: kind, URL: image.URL, Source: key})
	}
	for _, person := range record.People {
		book.Credits = append(book.Credits, Credit{Name: person.Name, Role: roleOfRecord(person.Role), Order: len(book.Credits)})
	}
	if record.Rating.Votes > 0 || record.Rating.Value > 0 {
		book.Ratings = []Rating{{Provider: key, Score: record.Rating.Value, Votes: int64(record.Rating.Votes)}}
	}
	return book
}

// roleOfRecord maps the generic record's person role to a book credit role; a role the book type has no word for is an author.
func roleOfRecord(role string) string {
	switch strings.ToLower(role) {
	case "narrator":
		return RoleNarrator
	case "translator":
		return RoleTranslator
	default:
		return RoleAuthor
	}
}

// askRecord reads a book from a definition by title and author: the first search result whose title matches.
func (s Service) askRecord(ctx context.Context, key string, hint Hint, failed *[]string) (Book, bool) {
	provider := s.recordProvider(key)
	if provider == nil || strings.TrimSpace(hint.Title) == "" {
		return Book{}, false
	}
	found, err := provider.Search(ctx, specprovider.Query{Title: hint.Title})
	if err != nil {
		s.warn(key, hint.Title, err)
		*failed = append(*failed, key)
		return Book{}, false
	}
	for _, record := range found {
		if strings.EqualFold(strings.TrimSpace(record.Title), strings.TrimSpace(hint.Title)) {
			if full, err := provider.Item(ctx, record.IDs[provider.Namespace()]); err == nil && full.Title != "" {
				record = full
			}
			return FromRecord(key, record), true
		}
	}
	return Book{}, false
}
