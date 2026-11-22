// Package fanart reads fanart.tv (webservice.fanart.tv): a film's clear logos, clear art, disc art, banners and
// backgrounds. It needs the owner's free personal key.
package fanart

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
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
	id := imdb
	if tmdb > 0 {
		id = strconv.FormatInt(tmdb, 10)
	}
	var movie Movie
	err := c.get(ctx, "/movies/", id, &movie)
	return movie, err
}

// Show is a series' pictures, by kind.
type Show struct {
	Name         string  `json:"name"`
	Logos        []Image `json:"hdtvlogo"`
	ClearArt     []Image `json:"hdclearart"`
	Banners      []Image `json:"tvbanner"`
	Posters      []Image `json:"tvposter"`
	Backgrounds  []Image `json:"showbackground"`
	Thumbs       []Image `json:"tvthumb"`
	SeasonPoster []Image `json:"seasonposter"`
	CharacterArt []Image `json:"characterart"`
}

// Show reads a series' pictures by TheTVDB's id.
func (c Client) Show(ctx context.Context, tvdb int64) (Show, error) {
	var show Show
	if tvdb <= 0 {
		return show, ErrNotFound
	}
	err := c.get(ctx, "/tv/", strconv.FormatInt(tvdb, 10), &show)
	return show, err
}

// Artist is a musician's pictures, by kind.
type Artist struct {
	Name        string  `json:"name"`
	Backgrounds []Image `json:"artistbackground"`
	Thumbs      []Image `json:"artistthumb"`
	Logos       []Image `json:"hdmusiclogo"`
	Banners     []Image `json:"musicbanner"`
}

// Artist reads a musician's pictures by MusicBrainz id.
func (c Client) Artist(ctx context.Context, mbid string) (Artist, error) {
	var artist Artist
	err := c.get(ctx, "/music/", mbid, &artist)
	return artist, err
}

// Album is the covers and disc art of the release groups of one MusicBrainz id, by release group.
type Album struct {
	Name   string `json:"name"`
	Albums map[string]struct {
		Covers []Image `json:"albumcover"`
		Discs  []Image `json:"cdart"`
	} `json:"albums"`
}

// Album reads an album's pictures by its release group's MusicBrainz id.
func (c Client) Album(ctx context.Context, releaseGroupMBID string) (Album, error) {
	var album Album
	err := c.get(ctx, "/music/albums/", releaseGroupMBID, &album)
	return album, err
}

func (c Client) get(ctx context.Context, path, id string, into any) error {
	if !c.Configured() || id == "" {
		return ErrNotFound
	}
	err := c.http.GetJSON(ctx, c.base+path+url.PathEscape(id)+"?"+url.Values{"api_key": {c.key()}}.Encode(), maxResponseBytes, into)
	switch {
	case errors.Is(err, httpx.ErrNotFound):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("fanart: %w", err)
	}
	return nil
}

// Best is the picture most worth showing: English or without text first, then the most liked.
func Best(images []Image) (Image, bool) {
	rank := func(image Image) int {
		switch image.Lang {
		case "en":
			return 0
		case "", "00":
			return 1
		}
		return 2
	}
	sorted := slices.Clone(images)
	slices.SortStableFunc(sorted, func(a, b Image) int {
		likesA, _ := strconv.Atoi(a.Likes)
		likesB, _ := strconv.Atoi(b.Likes)
		return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(likesB, likesA))
	})
	if len(sorted) == 0 || sorted[0].URL == "" {
		return Image{}, false
	}
	return sorted[0], true
}
