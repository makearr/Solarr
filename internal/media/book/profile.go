package book

import (
	"context"
	"slices"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/titlekey"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/openlibrary"
)

const (
	// maxOtherWorks bounds the books an author's page lists beside the ones the library holds.
	maxOtherWorks = 60
	// authorPageSize and maxAuthorPages bound how much of a keyed author's bibliography one look reads.
	authorPageSize = 100
	maxAuthorPages = 5
)

// AuthorWorks is a book type's part of an author's page: the author's other books at Open Library.
type AuthorWorks struct {
	Format Format
	People library.People
	Reader library.SeriesReader
	Source interface {
		ByAuthor(ctx context.Context, author string, limit int) ([]openlibrary.Doc, error)
		ByAuthorKey(ctx context.Context, authorID string, limit, offset int) ([]openlibrary.Doc, int, error)
	}
}

// authorQuery picks of an author's books those keep accepts (all when nil), at most limit (0: no bound); since stops
// reading a keyed bibliography, newest first, once it reaches books first published before that year.
type authorQuery struct {
	keep  func(openlibrary.Doc) bool
	limit int
	since int
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
	key, err := a.authorKey(ctx, personID)
	if err != nil {
		return nil, err
	}
	held := map[string]bool{}
	if titles, err := a.Reader.TitlesOf(ctx, a.Format.MediaType, categoryID); err == nil {
		for _, one := range titles {
			held[foldTitle(one.Title)] = true
		}
	}
	return a.otherBooks(ctx, person.Name, key, held)
}

// authorKey is the person's Open Library author id, stored from a book's credits; empty for someone never credited so.
func (a AuthorWorks) authorKey(ctx context.Context, personID int64) (string, error) {
	key, _, err := a.People.ExternalIDFor(ctx, personID, ProviderOpenLibraryAuthor)
	return key, err
}

// writtenBy are the author's books at Open Library that the query picks, one per title: by the author's key when
// known, since a name is shared by several writers, else by the name among each book's authors.
func (a AuthorWorks) writtenBy(ctx context.Context, name, key string, query authorQuery) ([]openlibrary.Doc, error) {
	seen := map[string]bool{}
	var written []openlibrary.Doc
	add := func(doc openlibrary.Doc) {
		title := foldTitle(doc.Title)
		if title == "" || seen[title] || (query.keep != nil && !query.keep(doc)) {
			return
		}
		seen[title] = true
		written = append(written, doc)
	}
	if key == "" {
		docs, err := a.Source.ByAuthor(ctx, name, authorPageSize)
		if err != nil {
			return nil, err
		}
		writer := PersonKey(name)
		for _, doc := range docs {
			if slices.ContainsFunc(doc.AuthorNames, func(name string) bool { return PersonKey(name) == writer }) {
				add(doc)
			}
		}
		return written, nil
	}
	for page := range maxAuthorPages {
		docs, found, err := a.Source.ByAuthorKey(ctx, key, authorPageSize, page*authorPageSize)
		if err != nil {
			return nil, err
		}
		older := false
		for _, doc := range docs {
			if query.limit > 0 && len(written) >= query.limit {
				break
			}
			older = older || (query.since > 0 && doc.FirstPublishYear > 0 && doc.FirstPublishYear < query.since)
			add(doc)
		}
		if older || len(docs) == 0 || (query.limit > 0 && len(written) >= query.limit) || (page+1)*authorPageSize >= found {
			break
		}
	}
	return written, nil
}

// otherBooks are the author's books Open Library lists that held does not hold, newest first.
func (a AuthorWorks) otherBooks(ctx context.Context, name, key string, held map[string]bool) ([]media.KnownFor, error) {
	docs, err := a.writtenBy(ctx, name, key, authorQuery{keep: func(doc openlibrary.Doc) bool { return !held[foldTitle(doc.Title)] }, limit: maxOtherWorks})
	if err != nil {
		return nil, err
	}
	var others []media.KnownFor
	for _, doc := range docs {
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
func foldTitle(title string) string { return titlekey.Fold(title, titlekey.Compact) }
