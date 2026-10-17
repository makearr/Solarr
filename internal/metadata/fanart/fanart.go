// Package fanart reads fanart.tv (webservice.fanart.tv): a film's clear logos, clear art, disc art, banners and
// backgrounds. It needs the owner's free personal key.
package fanart

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNotFound means fanart.tv has no pictures of the film.
var ErrNotFound = errors.New("fanart: not found")

const (
	baseURL          = "https://webservice.fanart.tv/v3"
	maxResponseBytes = 4 << 20
)

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Client asks fanart.tv with the owner's key.
type Client struct {
	http Getter
	key  func() string
	base string
}

// New builds a client; key answers the owner's key, empty while there is none.
func New(client Getter, key func() string) Client {
	return Client{http: client, key: key, base: baseURL}
}

// Configured says a key is stored, without which fanart.tv answers nothing.
func (c Client) Configured() bool { return c.key != nil && c.key() != "" }

// Image is one picture; Lang is its language, "" or "00" when it has no text, Likes how many people liked it.
type Image struct {
	URL   string `json:"url"`
	Lang  string `json:"lang"`
	Likes string `json:"likes"`
}

// Movie is a film's pictures, by kind.
type Movie struct {
	Name        string  `json:"name"`
	Logos       []Image `json:"hdmovielogo"`
	ClearArt    []Image `json:"hdmovieclearart"`
	Discs       []Image `json:"moviedisc"`
	Posters     []Image `json:"movieposter"`
	Backgrounds []Image `json:"moviebackground"`
	Banners     []Image `json:"moviebanner"`
	Thumbs      []Image `json:"moviethumb"`
}

// Movie reads a film's pictures by TMDB's id, else IMDb's.
func (c Client) Movie(ctx context.Context, tmdb int64, imdb string) (Movie, error) {
	if !c.Configured() {
		return Movie{}, ErrNotFound
	}
	id := imdb
	if tmdb > 0 {
		id = strconv.FormatInt(tmdb, 10)
	}
	if id == "" {
		return Movie{}, ErrNotFound
	}
	var movie Movie
	err := c.http.GetJSON(ctx, c.base+"/movies/"+url.PathEscape(id)+"?"+url.Values{"api_key": {c.key()}}.Encode(), maxResponseBytes, &movie)
	switch {
	case errors.Is(err, httpx.ErrNotFound):
		return Movie{}, ErrNotFound
	case err != nil:
		return Movie{}, fmt.Errorf("fanart: %w", err)
	}
	return movie, nil
}
