// Package lastfm reads Last.fm's public API with the owner's key: how many people listen to an album, and which artists sound alike.
package lastfm

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
)

const (
	baseURL          = "https://ws.audioscrobbler.com/2.0/"
	maxResponseBytes = 1 << 20
	// codeNotFound is Last.fm's error code for an album or artist it does not know.
	codeNotFound = 6
)

// ErrNotFound means Last.fm holds nothing under that name.
var ErrNotFound = errors.New("lastfm: not found")

// ErrNoKey means no key is stored, so nothing is asked.
var ErrNoKey = errors.New("lastfm: no api key")

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Client asks Last.fm with the owner's key.
type Client struct {
	http Getter
	key  func() string
}

// New builds a client; key is read at each call so a key saved later is used.
func New(client Getter, key func() string) Client { return Client{http: client, key: key} }

// Configured says a key is stored.
func (c Client) Configured() bool { return c.key != nil && c.key() != "" }

// Album is what Last.fm says of one album.
type Album struct {
	Name      string
	Artist    string
	MBID      string
	Listeners int64
	Plays     int64
}

// SimilarArtist is an artist Last.fm finds alike another, with its match from 0 to 1.
type SimilarArtist struct {
	Name  string
	MBID  string
	Match float64
}

type wireError struct {
	Code    int    `json:"error"`
	Message string `json:"message"`
}

func (e wireError) failed() bool { return e.Code != 0 }

func (c Client) ask(ctx context.Context, method string, values url.Values, into any) error {
	key := ""
	if c.key != nil {
		key = c.key()
	}
	if key == "" {
		return ErrNoKey
	}
	values.Set("method", method)
	values.Set("api_key", key)
	values.Set("format", "json")
	return c.http.GetJSON(ctx, baseURL+"?"+values.Encode(), maxResponseBytes, into)
}

// AlbumInfo reads the listeners and plays of an album by its artist and title.
func (c Client) AlbumInfo(ctx context.Context, artist, title string) (Album, error) {
	var found struct {
		wireError
		Album struct {
			Name      string `json:"name"`
			Artist    string `json:"artist"`
			MBID      string `json:"mbid"`
			Listeners string `json:"listeners"`
			Plays     string `json:"playcount"`
		} `json:"album"`
	}
	values := url.Values{"artist": {artist}, "album": {title}, "autocorrect": {"0"}}
	if err := c.ask(ctx, "album.getinfo", values, &found); err != nil {
		return Album{}, wrap("album.getinfo", err)
	}
	if found.failed() {
		if found.Code == codeNotFound {
			return Album{}, ErrNotFound
		}
		return Album{}, fmt.Errorf("lastfm: album.getinfo: error %d: %s", found.Code, found.Message)
	}
	album := Album{Name: found.Album.Name, Artist: found.Album.Artist, MBID: found.Album.MBID}
	album.Listeners, _ = strconv.ParseInt(found.Album.Listeners, 10, 64)
	album.Plays, _ = strconv.ParseInt(found.Album.Plays, 10, 64)
	return album, nil
}

// Similar lists the artists Last.fm finds alike one, best match first, at most limit.
func (c Client) Similar(ctx context.Context, artist string, limit int) ([]SimilarArtist, error) {
	var found struct {
		wireError
		Similar struct {
			Artist []struct {
				Name  string `json:"name"`
				MBID  string `json:"mbid"`
				Match string `json:"match"`
			} `json:"artist"`
		} `json:"similarartists"`
	}
	values := url.Values{"artist": {artist}, "limit": {strconv.Itoa(limit)}, "autocorrect": {"0"}}
	if err := c.ask(ctx, "artist.getsimilar", values, &found); err != nil {
		return nil, wrap("artist.getsimilar", err)
	}
	if found.failed() {
		if found.Code == codeNotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lastfm: artist.getsimilar: error %d: %s", found.Code, found.Message)
	}
	similar := make([]SimilarArtist, 0, len(found.Similar.Artist))
	for _, entry := range found.Similar.Artist {
		match, _ := strconv.ParseFloat(entry.Match, 64)
		similar = append(similar, SimilarArtist{Name: entry.Name, MBID: entry.MBID, Match: match})
	}
	return similar, nil
}

func wrap(method string, err error) error {
	if errors.Is(err, ErrNoKey) {
		return err
	}
	return fmt.Errorf("lastfm: %s: %w", method, err)
}
