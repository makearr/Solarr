package book

import (
	"context"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/mediaserver"
)

// FromServer is what a media server of the person's own holds of a book; the merge ranks it after every other source.
func FromServer(item mediaserver.Item, source string) Book {
	book := Book{
		Title: item.Title, Overview: item.Overview, Year: item.Year, Language: item.Language, Genres: item.Genres,
		IDs: IDs{ISBN: item.IDs["isbn"], ASIN: item.IDs["asin"]}, Sources: []string{source},
	}
	if item.RuntimeMS > 0 {
		book.RuntimeMin = int(item.RuntimeMS / 60000)
	}
	if len(item.Organisations) > 0 {
		book.Publisher = item.Organisations[0]
	}
	if item.Parent != nil && item.Parent.Title != "" {
		name, position, _ := strings.Cut(item.Parent.Title, " #")
		book.Series = &Series{Key: name, Name: name, Position: position}
	}
	order := 0
	for _, person := range item.People {
		role := RoleAuthor
		if person.Kind == "narrator" {
			role = RoleNarrator
		}
		book.Credits = append(book.Credits, Credit{Name: person.Name, Role: role, Order: order})
		order++
	}
	return book
}

// askLocal asks the media server of the source's kind the library is mapped to, by an id the book carries, else by its title and author.
func (s Service) askLocal(ctx context.Context, categoryID int64, source string, ids IDs, hint Hint) (Book, bool) {
	if s.Local == nil || categoryID == 0 {
		return Book{}, false
	}
	known := map[string]string{"isbn": ids.ISBN, "asin": ids.ASIN}
	item, found, err := s.Local.Ask(ctx, categoryID, strings.TrimPrefix(source, settings.LocalSourcePrefix), "book", mediaserver.Question{IDs: known, Title: hint.Title, Author: hint.Author})
	if err != nil {
		s.warn(source, ids.ISBN, err)
		return Book{}, false
	}
	if !found {
		return Book{}, false
	}
	book := FromServer(item, source)
	// An id from a file or another provider is never replaced by the server's.
	if ids.ISBN != "" {
		book.IDs.ISBN = ""
	}
	if ids.ASIN != "" {
		book.IDs.ASIN = ""
	}
	return book, true
}
