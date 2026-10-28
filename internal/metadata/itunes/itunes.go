// Package itunes reads Apple's audiobook catalogue (itunes.apple.com), keyless, as Audiobookshelf's iTunes provider does:
// titles, authors, descriptions, genres and large covers of audiobooks.
package itunes

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// ErrNotFound means iTunes knows no audiobook under that id.
var ErrNotFound = errors.New("itunes: not found")

const (
	baseURL          = "https://itunes.apple.com"
	maxResponseBytes = 4 << 20
	// SearchPageSize is the most one search answers.
	SearchPageSize = 20
)

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Client asks iTunes.
type Client struct {
	http Getter
	base string
}

// New builds a client over an httpx client carrying iTunes's own rate bucket.
func New(client Getter) Client { return Client{http: client, base: baseURL} }

// Audiobook is one audiobook as iTunes describes it.
type Audiobook struct {
	CollectionID   int64  `json:"collectionId"`
	Name           string `json:"collectionName"`
	Author         string `json:"artistName"`
	Description    string `json:"description"`
	ReleaseDate    string `json:"releaseDate"`
	Artwork100     string `json:"artworkUrl100"`
	Genre          string `json:"primaryGenreName"`
	CollectionView string `json:"collectionViewUrl"`
}

// Title is the name without the "(Unabridged)" tag iTunes appends.
func (a Audiobook) Title() string {
	name := strings.TrimSpace(a.Name)
	for _, tag := range []string{" (Unabridged)", " (Abridged)"} {
		name = strings.TrimSuffix(name, tag)
	}
	return name
}

// Abridged says iTunes tags the recording abridged.
func (a Audiobook) Abridged() bool { return strings.HasSuffix(strings.TrimSpace(a.Name), "(Abridged)") }

// Cover is the artwork at 600 pixels; iTunes serves any size by the name of the file.
func (a Audiobook) Cover() string {
	return strings.Replace(a.Artwork100, "100x100bb", "600x600bb", 1)
}

// Year is the year of release, 0 when unknown.
func (a Audiobook) Year() int {
	if len(a.ReleaseDate) < 4 {
		return 0
	}
	year, _ := strconv.Atoi(a.ReleaseDate[:4])
	return year
}

type answer struct {
	Results []Audiobook `json:"results"`
}

// Search finds audiobooks by title and, when given, author.
func (c Client) Search(ctx context.Context, title, author string, limit int) ([]Audiobook, error) {
	if limit <= 0 || limit > SearchPageSize {
		limit = SearchPageSize
	}
	query := url.Values{"term": {strings.TrimSpace(title + " " + author)}, "media": {"audiobook"}, "entity": {"audiobook"}, "limit": {strconv.Itoa(limit)}}
	var found answer
	if err := c.http.GetJSON(ctx, c.base+"/search?"+query.Encode(), maxResponseBytes, &found); err != nil {
		return nil, err
	}
	return found.Results, nil
}

// Lookup reads one audiobook by its collection id.
func (c Client) Lookup(ctx context.Context, id int64) (Audiobook, error) {
	var found answer
	if err := c.http.GetJSON(ctx, c.base+"/lookup?id="+strconv.FormatInt(id, 10)+"&entity=audiobook", maxResponseBytes, &found); err != nil {
		return Audiobook{}, err
	}
	if len(found.Results) == 0 {
		return Audiobook{}, ErrNotFound
	}
	return found.Results[0], nil
}
