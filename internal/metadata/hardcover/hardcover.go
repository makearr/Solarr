// Package hardcover reads Hardcover (hardcover.app) through its GraphQL API, which needs the owner's own free token:
// the readers' catalogue Readarr's successors and ReadMeABook take reading lists and books from.
package hardcover

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNoKey means no token is stored; ErrNotFound that Hardcover knows no book under that id.
var (
	ErrNoKey    = errors.New("hardcover: no API token")
	ErrNotFound = errors.New("hardcover: not found")
)

const (
	endpoint         = "https://api.hardcover.app/v1/graphql"
	maxResponseBytes = 4 << 20
	// SearchPageSize is the most one search answers.
	SearchPageSize = 10
)

// Doer is the one method of httpx.Client this package needs.
type Doer interface {
	Do(ctx context.Context, request *http.Request) (*http.Response, error)
}

// Client asks Hardcover with the owner's token.
type Client struct {
	http Doer
	key  func() string
	base string
}

// New builds a client; key answers the owner's token, empty while there is none.
func New(client Doer, key func() string) Client {
	return Client{http: client, key: key, base: endpoint}
}

// Configured says a token is stored.
func (c Client) Configured() bool { return c.key != nil && strings.TrimSpace(c.key()) != "" }

// Book is one book as Hardcover describes it.
type Book struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Subtitle     string   `json:"subtitle"`
	Description  string   `json:"description"`
	Pages        int      `json:"pages"`
	ReleaseYear  int      `json:"release_year"`
	Rating       float64  `json:"rating"`
	RatingsCount int64    `json:"ratings_count"`
	Slug         string   `json:"slug"`
	Authors      []string `json:"author_names"`
	Genres       []string `json:"genres"`
	ISBNs        []string `json:"isbns"`
	SeriesNames  []string `json:"series_names"`
	Image        struct {
		URL string `json:"url"`
	} `json:"image"`
}

// Cover is the book's picture, empty when it has none.
func (b Book) Cover() string { return b.Image.URL }

func (c Client) query(ctx context.Context, query string, variables map[string]any, into any) error {
	if !c.Configured() {
		return ErrNoKey
	}
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base, bytes.NewReader(body))
	if err != nil {
		return err
	}
	token := strings.TrimSpace(c.key())
	if !strings.HasPrefix(strings.ToLower(token), "bearer ") {
		token = "Bearer " + token
	}
	request.Header.Set("Authorization", token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(ctx, request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return err
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("hardcover: decode: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("hardcover: %s", envelope.Errors[0].Message)
	}
	return json.Unmarshal(envelope.Data, into)
}

// Search finds books by title and, when given, author.
func (c Client) Search(ctx context.Context, title, author string, limit int) ([]Book, error) {
	if limit <= 0 || limit > SearchPageSize {
		limit = SearchPageSize
	}
	var found struct {
		Search struct {
			Results struct {
				Hits []struct {
					Document Book `json:"document"`
				} `json:"hits"`
			} `json:"results"`
		} `json:"search"`
	}
	const search = `query ($q: String!, $n: Int!) { search(query: $q, query_type: "Book", per_page: $n, page: 1) { results } }`
	if err := c.query(ctx, search, map[string]any{"q": strings.TrimSpace(title + " " + author), "n": limit}, &found); err != nil {
		return nil, err
	}
	books := make([]Book, 0, len(found.Search.Results.Hits))
	for _, hit := range found.Search.Results.Hits {
		books = append(books, hit.Document)
	}
	return books, nil
}

// ChartBook is one book of a chart, with the editions whose ISBNs name it elsewhere.
type ChartBook struct {
	ID           int64   `json:"id"`
	Title        string  `json:"title"`
	ReleaseYear  int     `json:"release_year"`
	ReleaseDate  string  `json:"release_date"`
	Rating       float64 `json:"rating"`
	RatingsCount int64   `json:"ratings_count"`
	UsersCount   int64   `json:"users_count"`
	Image        struct {
		URL string `json:"url"`
	} `json:"image"`
	Contributions []struct {
		Author struct {
			Name string `json:"name"`
		} `json:"author"`
	} `json:"contributions"`
	PhysicalEdition *chartEdition `json:"default_physical_edition"`
	EbookEdition    *chartEdition `json:"default_ebook_edition"`
}

type chartEdition struct {
	ISBN13 string `json:"isbn_13"`
	ISBN10 string `json:"isbn_10"`
}

// Authors are the book's credited names, in Hardcover's order.
func (b ChartBook) Authors() []string {
	names := make([]string, 0, len(b.Contributions))
	for _, one := range b.Contributions {
		if one.Author.Name != "" {
			names = append(names, one.Author.Name)
		}
	}
	return names
}

// ISBNs are the ISBNs of the default print and ebook editions, 13 digits first.
func (b ChartBook) ISBNs() []string {
	var isbns []string
	for _, edition := range []*chartEdition{b.PhysicalEdition, b.EbookEdition} {
		if edition == nil {
			continue
		}
		for _, isbn := range []string{edition.ISBN13, edition.ISBN10} {
			if isbn != "" && !slices.Contains(isbns, isbn) {
				isbns = append(isbns, isbn)
			}
		}
	}
	return isbns
}

// chartFields is what a chart reads of each book.
const chartFields = `id title release_year release_date rating ratings_count users_count image { url } contributions { author { name } }
default_physical_edition { isbn_13 isbn_10 } default_ebook_edition { isbn_13 isbn_10 }`

// Trending reads one page of the books most read between two ISO days, most first.
func (c Client) Trending(ctx context.Context, from, to string, limit, offset int) ([]ChartBook, error) {
	var trending struct {
		BooksTrending struct {
			IDs []int64 `json:"ids"`
		} `json:"books_trending"`
	}
	const query = `query ($from: date!, $to: date!, $n: Int!, $o: Int!) { books_trending(from: $from, to: $to, limit: $n, offset: $o) { ids } }`
	if err := c.query(ctx, query, map[string]any{"from": from, "to": to, "n": limit, "o": offset}, &trending); err != nil {
		return nil, err
	}
	ids := trending.BooksTrending.IDs
	if len(ids) == 0 {
		return nil, nil
	}
	var found struct {
		Books []ChartBook `json:"books"`
	}
	if err := c.query(ctx, `query ($ids: [Int!]) { books(where: {id: {_in: $ids}}) { `+chartFields+` } }`, map[string]any{"ids": ids}, &found); err != nil {
		return nil, err
	}
	byID := make(map[int64]ChartBook, len(found.Books))
	for _, book := range found.Books {
		byID[book.ID] = book
	}
	books := make([]ChartBook, 0, len(ids))
	for _, id := range ids {
		if book, ok := byID[id]; ok {
			books = append(books, book)
		}
	}
	return books, nil
}

// TopRated reads one page of the best rated books with at least minRatings ratings, released by the year (0 for any), best first.
func (c Client) TopRated(ctx context.Context, minRatings int64, releasedBy, limit, offset int) ([]ChartBook, error) {
	where := map[string]any{"ratings_count": map[string]any{"_gte": minRatings}}
	if releasedBy > 0 {
		where["release_year"] = map[string]any{"_lte": releasedBy}
	}
	return c.chart(ctx, where, map[string]any{"rating": "desc"}, limit, offset)
}

// Upcoming reads one page of the books released after an ISO day, most awaited first.
func (c Client) Upcoming(ctx context.Context, after string, limit, offset int) ([]ChartBook, error) {
	return c.chart(ctx, map[string]any{"release_date": map[string]any{"_gt": after}}, map[string]any{"users_count": "desc"}, limit, offset)
}

func (c Client) chart(ctx context.Context, where, order map[string]any, limit, offset int) ([]ChartBook, error) {
	var found struct {
		Books []ChartBook `json:"books"`
	}
	query := `query ($where: books_bool_exp!, $order: [books_order_by!], $n: Int!, $o: Int!) { books(where: $where, order_by: $order, limit: $n, offset: $o) { ` + chartFields + ` } }`
	if err := c.query(ctx, query, map[string]any{"where": where, "order": order, "n": limit, "o": offset}, &found); err != nil {
		return nil, err
	}
	return found.Books, nil
}

// List is one of the signed-in account's lists.
type List struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// MyLists reads the lists of the account the token belongs to; read only.
func (c Client) MyLists(ctx context.Context) ([]List, error) {
	var found struct {
		Me []struct {
			Lists []List `json:"lists"`
		} `json:"me"`
	}
	if err := c.query(ctx, `{ me { lists(order_by: {id: asc}) { id name slug } } }`, nil, &found); err != nil {
		return nil, err
	}
	var lists []List
	for _, account := range found.Me {
		lists = append(lists, account.Lists...)
	}
	return lists, nil
}

// ListBook is one book on a list.
type ListBook struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Year  int    `json:"release_year"`
}

// ListBooks reads one page of a list's books in the list's own order.
func (c Client) ListBooks(ctx context.Context, listID int64, limit, offset int) ([]ListBook, error) {
	var found struct {
		Rows []struct {
			Book ListBook `json:"book"`
		} `json:"list_books"`
	}
	const query = `query ($id: Int!, $n: Int!, $o: Int!) { list_books(where: {list_id: {_eq: $id}}, order_by: {position: asc}, limit: $n, offset: $o) { book { id title release_year } } }`
	if err := c.query(ctx, query, map[string]any{"id": listID, "n": limit, "o": offset}, &found); err != nil {
		return nil, err
	}
	books := make([]ListBook, 0, len(found.Rows))
	for _, row := range found.Rows {
		books = append(books, row.Book)
	}
	return books, nil
}

// SeriesBook is one book of a Hardcover series with its place in it.
type SeriesBook struct {
	ID       int64
	Title    string
	Year     int
	Position float64
}

// SeriesBooks lists the books of the series named exactly like name, in series order; none when Hardcover has no such
// series. A book listed twice (several editions of its place) is kept once.
func (c Client) SeriesBooks(ctx context.Context, name string) ([]SeriesBook, error) {
	if !c.Configured() || strings.TrimSpace(name) == "" {
		return nil, nil
	}
	var found struct {
		Series []struct {
			Books []struct {
				Position *float64 `json:"position"`
				Book     struct {
					ID    int64  `json:"id"`
					Title string `json:"title"`
					Year  int    `json:"release_year"`
				} `json:"book"`
			} `json:"book_series"`
		} `json:"series"`
	}
	const query = `query ($name: String!) { series(where: {name: {_eq: $name}}, limit: 5) { book_series(order_by: {position: asc}) { position book { id title release_year } } } }`
	if err := c.query(ctx, query, map[string]any{"name": strings.TrimSpace(name)}, &found); err != nil {
		return nil, err
	}
	// Several series can share a name; the one with the most books is the real one.
	most := 0
	for index, series := range found.Series {
		if len(series.Books) > len(found.Series[most].Books) {
			most = index
		}
	}
	var books []SeriesBook
	seen := map[int64]bool{}
	for index, series := range found.Series {
		if index != most {
			continue
		}
		for _, row := range series.Books {
			if row.Book.ID == 0 || row.Book.Title == "" || seen[row.Book.ID] {
				continue
			}
			seen[row.Book.ID] = true
			book := SeriesBook{ID: row.Book.ID, Title: row.Book.Title, Year: row.Book.Year}
			if row.Position != nil {
				book.Position = *row.Position
			}
			books = append(books, book)
		}
	}
	return books, nil
}
