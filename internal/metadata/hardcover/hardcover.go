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
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("hardcover: status %d", response.StatusCode)
	}
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
