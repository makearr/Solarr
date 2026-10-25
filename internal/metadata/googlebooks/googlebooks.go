// Package googlebooks reads Google Books (googleapis.com/books/v1): volumes by id, ISBN or title and author, as
// Audiobookshelf and LazyLibrarian ask it. It answers without a key at a small shared quota; the owner's own free key
// raises it to their own.
package googlebooks

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNotFound means Google Books knows no volume under that id.
var ErrNotFound = errors.New("googlebooks: not found")

const (
	baseURL          = "https://www.googleapis.com/books/v1"
	maxResponseBytes = 8 << 20
	// SearchPageSize is the most one search answers; Google allows 40.
	SearchPageSize = 20
)

// volumePattern is the only volume id shape that reaches a request.
var volumePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{6,20}$`)

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Client asks Google Books, with the owner's key when there is one.
type Client struct {
	http Getter
	key  func() string
	base string
}

// New builds a client; key answers the owner's key, empty while there is none (Google then answers on its shared quota).
func New(client Getter, key func() string) Client {
	return Client{http: client, key: key, base: baseURL}
}

// Identifier is one of a volume's industry identifiers.
type Identifier struct {
	Type       string `json:"type"`
	Identifier string `json:"identifier"`
}

// Volume is one book as Google Books describes it.
type Volume struct {
	ID         string `json:"id"`
	VolumeInfo struct {
		Title         string       `json:"title"`
		Subtitle      string       `json:"subtitle"`
		Authors       []string     `json:"authors"`
		Publisher     string       `json:"publisher"`
		PublishedDate string       `json:"publishedDate"`
		Description   string       `json:"description"`
		Identifiers   []Identifier `json:"industryIdentifiers"`
		PageCount     int          `json:"pageCount"`
		Categories    []string     `json:"categories"`
		AverageRating float64      `json:"averageRating"`
		RatingsCount  int64        `json:"ratingsCount"`
		Language      string       `json:"language"`
		ImageLinks    struct {
			Thumbnail  string `json:"thumbnail"`
			Small      string `json:"small"`
			Medium     string `json:"medium"`
			Large      string `json:"large"`
			ExtraLarge string `json:"extraLarge"`
		} `json:"imageLinks"`
	} `json:"volumeInfo"`
}

// ISBN is the volume's ISBN-13, else its ISBN-10, else empty.
func (v Volume) ISBN() string {
	for _, kind := range []string{"ISBN_13", "ISBN_10"} {
		for _, identifier := range v.VolumeInfo.Identifiers {
			if identifier.Type == kind && identifier.Identifier != "" {
				return identifier.Identifier
			}
		}
	}
	return ""
}

// Cover is the largest picture the volume offers, over https.
func (v Volume) Cover() string {
	links := v.VolumeInfo.ImageLinks
	for _, address := range []string{links.ExtraLarge, links.Large, links.Medium, links.Small, links.Thumbnail} {
		if address != "" {
			return strings.Replace(address, "http://", "https://", 1)
		}
	}
	return ""
}

// Year is the year the volume was published, 0 when unknown.
func (v Volume) Year() int {
	if len(v.VolumeInfo.PublishedDate) < 4 {
		return 0
	}
	year, _ := strconv.Atoi(v.VolumeInfo.PublishedDate[:4])
	return year
}

// Volume reads one volume by id.
func (c Client) Volume(ctx context.Context, id string) (Volume, error) {
	if !volumePattern.MatchString(id) {
		return Volume{}, ErrNotFound
	}
	var volume Volume
	return volume, c.get(ctx, "/volumes/"+id, url.Values{}, &volume)
}

// Search finds volumes by title and, when given, author; an ISBN as the title asks by ISBN. An empty answer is not an
// error.
func (c Client) Search(ctx context.Context, title, author string, limit int) ([]Volume, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, nil
	}
	term := "intitle:" + title
	if isbn := strings.ReplaceAll(title, "-", ""); isISBN(isbn) {
		term = "isbn:" + isbn
	} else if author != "" {
		term += " inauthor:" + author
	}
	if limit <= 0 || limit > SearchPageSize {
		limit = SearchPageSize
	}
	var found struct {
		Items []Volume `json:"items"`
	}
	query := url.Values{"q": {term}, "maxResults": {strconv.Itoa(limit)}, "printType": {"books"}}
	if err := c.get(ctx, "/volumes", query, &found); err != nil {
		return nil, err
	}
	return found.Items, nil
}

// ByISBN reads the first volume Google files under an ISBN.
func (c Client) ByISBN(ctx context.Context, isbn string) (Volume, error) {
	isbn = strings.ReplaceAll(strings.TrimSpace(isbn), "-", "")
	if !isISBN(isbn) {
		return Volume{}, ErrNotFound
	}
	found, err := c.Search(ctx, isbn, "", 1)
	if err != nil {
		return Volume{}, err
	}
	if len(found) == 0 {
		return Volume{}, ErrNotFound
	}
	return found[0], nil
}

func isISBN(value string) bool {
	if len(value) != 10 && len(value) != 13 {
		return false
	}
	for index, r := range value {
		if (r < '0' || r > '9') && (r != 'X' || index != 9 || len(value) != 10) {
			return false
		}
	}
	return true
}

func (c Client) get(ctx context.Context, path string, query url.Values, into any) error {
	if c.key != nil {
		if key := c.key(); key != "" {
			query.Set("key", key)
		}
	}
	address := c.base + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	err := c.http.GetJSON(ctx, address, maxResponseBytes, into)
	switch {
	case errors.Is(err, httpx.ErrNotFound):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("googlebooks: %w", err)
	}
	return nil
}
