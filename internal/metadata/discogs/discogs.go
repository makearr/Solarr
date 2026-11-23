// Package discogs reads Discogs' database with the owner's personal token: the community's rating of an album.
package discogs

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	baseURL          = "https://api.discogs.com"
	maxResponseBytes = 1 << 20
)

// ErrNotFound means Discogs holds no release of that artist and title.
var ErrNotFound = errors.New("discogs: not found")

// ErrNoKey means no token is stored, so nothing is asked.
var ErrNoKey = errors.New("discogs: no token")

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Client asks Discogs with the owner's token.
type Client struct {
	http Getter
	key  func() string
}

// New builds a client; key is read at each call so a token saved later is used.
func New(client Getter, key func() string) Client { return Client{http: client, key: key} }

// Configured says a token is stored.
func (c Client) Configured() bool { return c.key != nil && c.key() != "" }

// Release is what the Discogs community says of one release.
type Release struct {
	ID int64
	// Rating is the members' average from 0 to 5 and Votes how many gave one.
	Rating float64
	Votes  int64
	Have   int64
	Want   int64
}

// Find searches the release by exact artist and title and reads its page for the community rating.
func (c Client) Find(ctx context.Context, artist, title string) (Release, error) {
	token := ""
	if c.key != nil {
		token = c.key()
	}
	if token == "" {
		return Release{}, ErrNoKey
	}
	var found struct {
		Results []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"results"`
	}
	values := url.Values{"type": {"release"}, "artist": {artist}, "release_title": {title}, "per_page": {"5"}, "token": {token}}
	if err := c.http.GetJSON(ctx, baseURL+"/database/search?"+values.Encode(), maxResponseBytes, &found); err != nil {
		return Release{}, fmt.Errorf("discogs: search: %w", err)
	}
	for _, hit := range found.Results {
		// Discogs titles a hit "Artist - Title".
		name, album, split := strings.Cut(hit.Title, " - ")
		if !split || !strings.EqualFold(name, artist) || !strings.EqualFold(album, title) {
			continue
		}
		var page struct {
			ID        int64 `json:"id"`
			Community struct {
				Have   int64 `json:"have"`
				Want   int64 `json:"want"`
				Rating struct {
					Count   int64   `json:"count"`
					Average float64 `json:"average"`
				} `json:"rating"`
			} `json:"community"`
		}
		address := baseURL + "/releases/" + strconv.FormatInt(hit.ID, 10) + "?token=" + url.QueryEscape(token)
		if err := c.http.GetJSON(ctx, address, maxResponseBytes, &page); err != nil {
			return Release{}, fmt.Errorf("discogs: release %d: %w", hit.ID, err)
		}
		return Release{ID: page.ID, Rating: page.Community.Rating.Average, Votes: page.Community.Rating.Count, Have: page.Community.Have, Want: page.Community.Want}, nil
	}
	return Release{}, ErrNotFound
}
