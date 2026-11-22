// Package deezer reads Deezer's public catalogue (no key): an album found by artist and title, and the fans it has.
package deezer

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	baseURL          = "https://api.deezer.com"
	maxResponseBytes = 1 << 20
)

// ErrNotFound means Deezer holds no album of that artist and title.
var ErrNotFound = errors.New("deezer: not found")

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Client asks Deezer.
type Client struct{ http Getter }

// New builds a client.
func New(client Getter) Client { return Client{http: client} }

// Album is what Deezer says of one album: its fans, whether it is marked explicit, and its cover.
type Album struct {
	ID       int64
	Title    string
	Artist   string
	Fans     int64
	Explicit bool
	Cover    string
}

type wireAlbum struct {
	ID             int64  `json:"id"`
	Title          string `json:"title"`
	Fans           int64  `json:"fans"`
	CoverXL        string `json:"cover_xl"`
	ExplicitLyrics bool   `json:"explicit_lyrics"`
	Artist         struct {
		Name string `json:"name"`
	} `json:"artist"`
}

// Find searches the album by exact artist and title (never title alone) and reads its page for the fans.
func (c Client) Find(ctx context.Context, artist, title string) (Album, error) {
	var found struct {
		Data []wireAlbum `json:"data"`
	}
	query := fmt.Sprintf(`artist:%q album:%q`, artist, title)
	if err := c.http.GetJSON(ctx, baseURL+"/search/album?limit=5&q="+url.QueryEscape(query), maxResponseBytes, &found); err != nil {
		return Album{}, fmt.Errorf("deezer: search: %w", err)
	}
	for _, hit := range found.Data {
		if !strings.EqualFold(hit.Artist.Name, artist) || !strings.EqualFold(hit.Title, title) {
			continue
		}
		var page wireAlbum
		if err := c.http.GetJSON(ctx, baseURL+"/album/"+strconv.FormatInt(hit.ID, 10), maxResponseBytes, &page); err != nil {
			return Album{}, fmt.Errorf("deezer: album %d: %w", hit.ID, err)
		}
		return Album{ID: page.ID, Title: page.Title, Artist: hit.Artist.Name, Fans: page.Fans, Explicit: page.ExplicitLyrics, Cover: page.CoverXL}, nil
	}
	return Album{}, ErrNotFound
}
