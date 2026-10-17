// Package mdblist reads MDBList (api.mdblist.com): a film's ratings from every site that rates it, in one request. It
// needs the owner's free key and allows 1,000 requests a day on the free tier.
package mdblist

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNotFound means MDBList knows no film under the id.
var ErrNotFound = errors.New("mdblist: not found")

const (
	baseURL          = "https://api.mdblist.com"
	maxResponseBytes = 4 << 20
)

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Client asks MDBList with the owner's key.
type Client struct {
	http Getter
	key  func() string
	base string
}

// New builds a client; key answers the owner's key, empty while there is none.
func New(client Getter, key func() string) Client {
	return Client{http: client, key: key, base: baseURL}
}

// Configured says a key is stored, without which MDBList answers nothing.
func (c Client) Configured() bool { return c.key != nil && c.key() != "" }

// Rating is one site's rating: Value on the site's own scale, Score on MDBList's 0 to 100.
type Rating struct {
	Source string  `json:"source"`
	Value  float64 `json:"value"`
	Score  float64 `json:"score"`
	Votes  int64   `json:"votes"`
}

// Media is a film as MDBList knows it.
type Media struct {
	Title   string   `json:"title"`
	Year    int      `json:"year"`
	IMDbID  string   `json:"imdb_id"`
	Ratings []Rating `json:"ratings"`
}

// Movie reads a film by TMDB's id, else IMDb's.
func (c Client) Movie(ctx context.Context, tmdb int64, imdb string) (Media, error) {
	if !c.Configured() {
		return Media{}, ErrNotFound
	}
	var path string
	switch {
	case tmdb > 0:
		path = "/tmdb/movie/" + strconv.FormatInt(tmdb, 10) + "/"
	case imdb != "":
		path = "/imdb/movie/" + url.PathEscape(imdb) + "/"
	default:
		return Media{}, ErrNotFound
	}
	var media Media
	err := c.http.GetJSON(ctx, c.base+path+"?"+url.Values{"apikey": {c.key()}}.Encode(), maxResponseBytes, &media)
	switch {
	case errors.Is(err, httpx.ErrNotFound):
		return Media{}, ErrNotFound
	case err != nil:
		return Media{}, fmt.Errorf("mdblist: %w", err)
	}
	return media, nil
}
