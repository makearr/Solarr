package book

import (
	"context"
	"slices"
	"strings"
	"unicode"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/openlibrary"
)

// maxOtherWorks bounds the books an author's page lists beside the ones the library holds.
const maxOtherWorks = 60

// AuthorWorks is a book type's part of an author's page: the author's other books at Open Library.
type AuthorWorks struct {
	Format Format
	People library.People
	Reader library.SeriesReader
	Source interface {
		ByAuthor(ctx context.Context, author string, limit int) ([]openlibrary.Doc, error)
	}
}

// KnownFor implements media.Profiling: the author's books the library does not hold, newest first.
func (a AuthorWorks) KnownFor(ctx context.Context, personID, categoryID int64) ([]media.KnownFor, error) {
	if a.Source == nil {
		return nil, nil
	}
	person, err := a.People.Person(ctx, personID)
	if err != nil {
		return nil, err
	}
	held := map[string]bool{}
	if titles, err := a.Reader.TitlesOf(ctx, a.Format.MediaType, categoryID); err == nil {
		for _, one := range titles {
			held[foldTitle(one.Title)] = true
		}
	}
	return a.otherBooks(ctx, person.Name, held)
}

// writtenBy are the books Open Library lists for the name that name it among their authors, one per title.
func (a AuthorWorks) writtenBy(ctx context.Context, name string) ([]openlibrary.Doc, error) {
	docs, err := a.Source.ByAuthor(ctx, name, 100)
	if err != nil {
		return nil, err
	}
	writer := PersonKey(name)
	seen := map[string]bool{}
	var written []openlibrary.Doc
	for _, doc := range docs {
		key := foldTitle(doc.Title)
		if key == "" || seen[key] || !slices.ContainsFunc(doc.AuthorNames, func(name string) bool { return PersonKey(name) == writer }) {
			continue
		}
		seen[key] = true
		written = append(written, doc)
	}
	return written, nil
}

// otherBooks are the author's books Open Library lists that held does not hold, newest first.
func (a AuthorWorks) otherBooks(ctx context.Context, name string, held map[string]bool) ([]media.KnownFor, error) {
	docs, err := a.writtenBy(ctx, name)
	if err != nil {
		return nil, err
	}
	var others []media.KnownFor
	for _, doc := range docs {
		key := foldTitle(doc.Title)
		if held[key] {
			continue
		}
		held[key] = true
		others = append(others, media.KnownFor{
			Provider: ProviderOpenLibrary, ExternalID: doc.WorkID(), Kind: a.Format.MediaType, Title: doc.Title,
			Year: int64(doc.FirstPublishYear), PosterURL: openlibrary.CoverURL(doc.CoverID), Role: RoleAuthor,
		})
	}
	slices.SortStableFunc(others, func(x, y media.KnownFor) int { return int(y.Year - x.Year) })
	if len(others) > maxOtherWorks {
		others = others[:maxOtherWorks]
	}
	return others, nil
}

// foldTitle is a title's letters and digits, so "Dune!" and "dune" are one.
func foldTitle(title string) string {
	var folded strings.Builder
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			folded.WriteRune(r)
		}
	}
	return folded.String()
}
