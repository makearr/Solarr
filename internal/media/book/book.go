// Package book is the books and audiobooks modules: two media types over one implementation, as Readarr ran one code
// base for both. A book is a work without parts whose one wanted item needs its files (an ebook's file, an audiobook's
// chapters), read from Open Library, Google Books and, for audiobooks, Audible through Audnexus, steered by Wikidata and
// merged field by field (ADR-8). What differs between the two is the Format; core sees works and items only (ADR-23).
package book

import "strings"

// Provider names, as stored in work_external_id and person_external_id.
const (
	ProviderOpenLibrary = "openlibrary"
	ProviderGoogleBooks = "googlebooks"
	ProviderAudible     = "audible"
	ProviderISBN        = "isbn"
	ProviderGoodreads   = "goodreads"
	ProviderHardcover   = "hardcover"
	ProviderITunes      = "itunes"
	ProviderFantLab     = "fantlab"
	ProviderWikidata    = "wikidata"
	// ProviderSeries keeps a book series on its collection row, by Wikidata's item or the provider's own name for it.
	ProviderSeries = "book:series"
	// ProviderPerson names an author or narrator by their name, so one writer is one person in every book library.
	ProviderPerson = "book:person"
)

// Roles a book stores, in the credit table's vocabulary.
const (
	RoleAuthor     = "author"
	RoleNarrator   = "narrator"
	RoleTranslator = "translator"
)

// Status is where a book stands before it is out.
type Status string

// A book is announced until its publication date, then published.
const (
	StatusAnnounced Status = "announced"
	StatusPublished Status = "published"
)

// IDs are the identifiers one book carries at the providers; empty means unknown.
type IDs struct {
	OpenLibrary string
	GoogleBooks string
	ASIN        string
	ISBN        string
	Goodreads   string
	Hardcover   string
	ITunes      string
	FantLab     string
}

// Empty says no provider id is known.
func (ids IDs) Empty() bool { return ids == IDs{} }

// Credit is one person's part in a book.
type Credit struct {
	Name  string
	Image string
	Role  string
	Order int
}

// Image is one picture of a book: its cover, kept as a "poster" as every work's picture is.
type Image struct {
	Kind   string `json:"kind"`
	URL    string `json:"url"`
	Source string `json:"source,omitempty"`
}

// Rating is one provider's score, on a scale of 0 to 10.
type Rating struct {
	Provider string
	Score    float64
	Votes    int64
}

// Series is the series a book belongs to, with the book's place in it ("1", "2.5").
type Series struct {
	// Key is Wikidata's item when it names the series, else the provider's id or the name itself.
	Key      string
	Name     string
	Position string
}

// Book is one book, merged from every source that knew it. Published is yyyy-MM-dd, yyyy-MM or yyyy, or empty.
type Book struct {
	IDs      IDs
	Title    string
	Subtitle string
	Aliases  []string
	Overview string
	Year     int
	// Published is when this edition or reading came out; Year is the work's first publication.
	Published string
	Language  string
	Publisher string
	Pages     int
	// RuntimeMin is an audiobook's length.
	RuntimeMin int
	Abridged   bool
	Genres     []string
	Ratings    []Rating
	Images     []Image
	Credits    []Credit
	Series     *Series
	// Sources names the providers that supplied it, in the order they were read.
	Sources []string
	// spareGenres are genres to fall back on when no provider names any: Wikidata's.
	spareGenres []string
}

// Authors are the book's authors' names, in credit order.
func (b Book) Authors() []string {
	var names []string
	for _, credit := range b.Credits {
		if credit.Role == RoleAuthor {
			names = append(names, credit.Name)
		}
	}
	return names
}

// PersonKey is the id a person is kept under: their name, lower case, its words single spaced and its dots gone, so
// "J.R.R. Tolkien" and "J. R. R. Tolkien" are one writer.
func PersonKey(name string) string {
	cleaned := strings.NewReplacer(".", " ", ",", " ").Replace(strings.ToLower(name))
	return strings.Join(strings.Fields(cleaned), " ")
}
