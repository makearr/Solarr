// Package openlibrary reads Open Library (openlibrary.org): works, their editions and their authors, keyless. It is
// what Audiobookshelf and LazyLibrarian ask for books, and the book libraries' lead provider: its work id names a book.
package openlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNotFound means Open Library knows nothing under that id.
var ErrNotFound = errors.New("openlibrary: not found")

const (
	baseURL          = "https://openlibrary.org"
	coversURL        = "https://covers.openlibrary.org"
	maxResponseBytes = 8 << 20
	// searchFields are what a search answers of each work: enough to add one without asking again.
	searchFields = "key,title,subtitle,author_name,author_key,first_publish_year,isbn,publisher,number_of_pages_median," +
		"language,cover_i,subject,edition_count,ratings_average,ratings_count,id_goodreads,id_google,id_amazon"
)

// workPattern, authorPattern and editionPattern are the only id shapes that reach a request.
var (
	workPattern    = regexp.MustCompile(`^OL[0-9]+W$`)
	authorPattern  = regexp.MustCompile(`^OL[0-9]+A$`)
	editionPattern = regexp.MustCompile(`^OL[0-9]+M$`)
	isbnPattern    = regexp.MustCompile(`^(?:[0-9]{9}[0-9X]|[0-9]{13})$`)
)

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Client asks Open Library.
type Client struct {
	http Getter
	base string
}

// New builds a client over an httpx client carrying Open Library's own rate bucket.
func New(client Getter) Client { return Client{http: client, base: baseURL} }

// IsWorkID says id is an Open Library work id ("OL27482W").
func IsWorkID(id string) bool { return workPattern.MatchString(id) }

// Doc is one work a search found, with what its editions say together.
type Doc struct {
	Key              string   `json:"key"`
	Title            string   `json:"title"`
	Subtitle         string   `json:"subtitle"`
	AuthorNames      []string `json:"author_name"`
	AuthorKeys       []string `json:"author_key"`
	FirstPublishYear int      `json:"first_publish_year"`
	ISBN             []string `json:"isbn"`
	Publishers       []string `json:"publisher"`
	Pages            int      `json:"number_of_pages_median"`
	Languages        []string `json:"language"`
	CoverID          int64    `json:"cover_i"`
	Subjects         []string `json:"subject"`
	EditionCount     int      `json:"edition_count"`
	RatingsAverage   float64  `json:"ratings_average"`
	RatingsCount     int64    `json:"ratings_count"`
	Goodreads        []string `json:"id_goodreads"`
	Google           []string `json:"id_google"`
	Amazon           []string `json:"id_amazon"`
}

// WorkID is the doc's work id, "OL27482W".
func (d Doc) WorkID() string { return strings.TrimPrefix(d.Key, "/works/") }

// Text is Open Library's description or bio, which is a string or an object holding one.
type Text string

// UnmarshalJSON implements json.Unmarshaler.
func (t *Text) UnmarshalJSON(raw []byte) error {
	var plain string
	if err := json.Unmarshal(raw, &plain); err == nil {
		*t = Text(plain)
		return nil
	}
	var typed struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &typed); err != nil {
		return err
	}
	*t = Text(typed.Value)
	return nil
}

// Work is one work as Open Library describes it.
type Work struct {
	Key            string   `json:"key"`
	Title          string   `json:"title"`
	Subtitle       string   `json:"subtitle"`
	Description    Text     `json:"description"`
	Subjects       []string `json:"subjects"`
	Covers         []int64  `json:"covers"`
	FirstPublished string   `json:"first_publish_date"`
	Authors        []struct {
		Author struct {
			Key string `json:"key"`
		} `json:"author"`
	} `json:"authors"`
}

// AuthorIDs are the work's authors' ids, "OL26320A", in Open Library's order.
func (w Work) AuthorIDs() []string {
	var ids []string
	for _, author := range w.Authors {
		if id := strings.TrimPrefix(author.Author.Key, "/authors/"); authorPattern.MatchString(id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// Author is one author.
type Author struct {
	Key       string            `json:"key"`
	Name      string            `json:"name"`
	BirthDate string            `json:"birth_date"`
	DeathDate string            `json:"death_date"`
	Bio       Text              `json:"bio"`
	Photos    []int64           `json:"photos"`
	RemoteIDs map[string]string `json:"remote_ids"`
}

// Edition is one printing of a work.
type Edition struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Publishers  []string `json:"publishers"`
	PublishDate string   `json:"publish_date"`
	ISBN13      []string `json:"isbn_13"`
	ISBN10      []string `json:"isbn_10"`
	Pages       int      `json:"number_of_pages"`
	Format      string   `json:"physical_format"`
	Covers      []int64  `json:"covers"`
	Works       []struct {
		Key string `json:"key"`
	} `json:"works"`
	Languages []struct {
		Key string `json:"key"`
	} `json:"languages"`
}

// WorkID is the edition's work id, empty when it names none.
func (e Edition) WorkID() string {
	for _, work := range e.Works {
		if id := strings.TrimPrefix(work.Key, "/works/"); workPattern.MatchString(id) {
			return id
		}
	}
	return ""
}

// SearchPageSize is how many works one search answers at most.
const SearchPageSize = 20

// Search finds works by title and, when given, author; an empty answer is not an error.
func (c Client) Search(ctx context.Context, title, author string, limit int) ([]Doc, error) {
	query := url.Values{"fields": {searchFields}}
	if limit <= 0 || limit > SearchPageSize {
		limit = SearchPageSize
	}
	query.Set("limit", strconv.Itoa(limit))
	switch {
	case isbnPattern.MatchString(strings.ReplaceAll(title, "-", "")):
		query.Set("isbn", strings.ReplaceAll(title, "-", ""))
	case author != "":
		query.Set("title", title)
		query.Set("author", author)
	default:
		query.Set("q", title)
	}
	var found struct {
		Docs []Doc `json:"docs"`
	}
	if err := c.get(ctx, "/search.json?"+query.Encode(), &found); err != nil {
		return nil, err
	}
	return found.Docs, nil
}

// ByAuthor finds the works an author wrote, the most published first.
func (c Client) ByAuthor(ctx context.Context, author string, limit int) ([]Doc, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	query := url.Values{"fields": {searchFields}, "limit": {strconv.Itoa(limit)}, "sort": {"editions"}, "author": {author}}
	var found struct {
		Docs []Doc `json:"docs"`
	}
	if err := c.get(ctx, "/search.json?"+query.Encode(), &found); err != nil {
		return nil, err
	}
	return found.Docs, nil
}

// Doc reads what the search index says of one work: its editions' ids, publishers and pages in one request.
func (c Client) Doc(ctx context.Context, workID string) (Doc, error) {
	if !workPattern.MatchString(workID) {
		return Doc{}, ErrNotFound
	}
	query := url.Values{"q": {"key:/works/" + workID}, "fields": {searchFields}, "limit": {"1"}}
	var found struct {
		Docs []Doc `json:"docs"`
	}
	if err := c.get(ctx, "/search.json?"+query.Encode(), &found); err != nil {
		return Doc{}, err
	}
	if len(found.Docs) == 0 {
		return Doc{}, ErrNotFound
	}
	return found.Docs[0], nil
}

// Work reads one work by id.
func (c Client) Work(ctx context.Context, workID string) (Work, error) {
	if !workPattern.MatchString(workID) {
		return Work{}, ErrNotFound
	}
	var work Work
	return work, c.get(ctx, "/works/"+workID+".json", &work)
}

// Author reads one author by id.
func (c Client) Author(ctx context.Context, authorID string) (Author, error) {
	if !authorPattern.MatchString(authorID) {
		return Author{}, ErrNotFound
	}
	var author Author
	return author, c.get(ctx, "/authors/"+authorID+".json", &author)
}

// ByISBN reads the edition an ISBN names, which names its work.
func (c Client) ByISBN(ctx context.Context, isbn string) (Edition, error) {
	isbn = strings.ReplaceAll(strings.TrimSpace(isbn), "-", "")
	if !isbnPattern.MatchString(isbn) {
		return Edition{}, ErrNotFound
	}
	var edition Edition
	return edition, c.get(ctx, "/isbn/"+isbn+".json", &edition)
}

// Edition reads one edition by id.
func (c Client) Edition(ctx context.Context, editionID string) (Edition, error) {
	if !editionPattern.MatchString(editionID) {
		return Edition{}, ErrNotFound
	}
	var edition Edition
	return edition, c.get(ctx, "/books/"+editionID+".json", &edition)
}

// CoverURL is the large picture of a cover id, empty for none.
func CoverURL(coverID int64) string {
	if coverID <= 0 {
		return ""
	}
	return coversURL + "/b/id/" + strconv.FormatInt(coverID, 10) + "-L.jpg"
}

// AuthorPhotoURL is an author's photo by the author's id; default=false answers 404 rather than a blank picture.
func AuthorPhotoURL(authorID string) string {
	if !authorPattern.MatchString(authorID) {
		return ""
	}
	return coversURL + "/a/olid/" + authorID + "-M.jpg?default=false"
}

func (c Client) get(ctx context.Context, path string, into any) error {
	err := c.http.GetJSON(ctx, c.base+path, maxResponseBytes, into)
	switch {
	case errors.Is(err, httpx.ErrNotFound):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("openlibrary: %w", err)
	}
	return nil
}
