// Package lrclib reads LRCLIB (lrclib.net), keyless: the synced and plain lyrics of a recording, found by artist, title,
// album and length.
package lrclib

import (
	"context"
	"net/url"
	"strconv"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

const (
	baseURL          = "https://lrclib.net"
	maxResponseBytes = 1 << 20
)

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Client asks LRCLIB.
type Client struct {
	http Getter
	base string
}

// New builds a client over an httpx client carrying LRCLIB's own rate bucket.
func New(client Getter) Client { return Client{http: client, base: baseURL} }

// Lyrics is what LRCLIB holds for one recording; Synced is the LRC text with a time before each line.
type Lyrics struct {
	Plain        string `json:"plainLyrics"`
	Synced       string `json:"syncedLyrics"`
	Instrumental bool   `json:"instrumental"`
}

// Empty says there is nothing to write: an instrumental, or a record without text.
func (l Lyrics) Empty() bool { return l.Instrumental || (l.Plain == "" && l.Synced == "") }

// Track names the recording asked about; Album and DurationSeconds sharpen the match and may be zero.
type Track struct {
	Artist, Title, Album string
	DurationSeconds      int64
}

// Get answers the lyrics of the recording, and false when LRCLIB has none.
func (c Client) Get(ctx context.Context, track Track) (Lyrics, bool, error) {
	query := url.Values{"artist_name": {track.Artist}, "track_name": {track.Title}}
	if track.Album != "" {
		query.Set("album_name", track.Album)
	}
	if track.DurationSeconds > 0 {
		query.Set("duration", strconv.FormatInt(track.DurationSeconds, 10))
	}
	var lyrics Lyrics
	if err := c.http.GetJSON(ctx, c.base+"/api/get?"+query.Encode(), maxResponseBytes, &lyrics); err != nil {
		if httpx.IsNotFound(err) {
			return Lyrics{}, false, nil
		}
		return Lyrics{}, false, err
	}
	return lyrics, !lyrics.Empty(), nil
}
